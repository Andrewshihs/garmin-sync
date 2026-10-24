package main

// mcp.go implements a Model Context Protocol (MCP) server over stdio,
// exposing Garmin data as tools for AI agents. Protocol: JSON-RPC 2.0,
// version 2024-11-05. Transport: newline-delimited JSON on stdin/stdout.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	garminactivities "github.com/Andrewshihs/garmin-sync/internal/activities"
	"github.com/Andrewshihs/garmin-sync/internal/auth"
	"github.com/Andrewshihs/garmin-sync/internal/client"
	"github.com/Andrewshihs/garmin-sync/internal/config"
	garminhealth "github.com/Andrewshihs/garmin-sync/internal/health"
	"github.com/Andrewshihs/garmin-sync/internal/personalapi"
	garmintraining "github.com/Andrewshihs/garmin-sync/internal/training"
	garminweight "github.com/Andrewshihs/garmin-sync/internal/weight"
	"github.com/spf13/cobra"
)

// ---- MCP tool definition ----

type mcpToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
	Handler     func(ctx context.Context, args map[string]any) (string, error)
}

// ---- JSON-RPC 2.0 types ----

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  any         `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ---- MCP server command ----

func newMCPCmd(opts *globalOptions) *cobra.Command {
	var mcpProfile, mcpRegion string

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start an MCP server on stdio",
		Long: `Start a Model Context Protocol (MCP) server that exposes Garmin data
as tools for AI agents. Runs on stdio (the standard transport for Claude
Desktop, Claude Code, ZCode and other local MCP clients).

Claude Desktop / Claude Code / ZCode configuration:
  {
    "mcpServers": {
      "garmin": {
        "command": "/usr/local/bin/garmin",
        "args": ["mcp", "--profile", "cn", "--region", "cn"]
      }
    }
  }`,
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := orDefault(mcpProfile, opts.Profile)
			if profile == "" {
				profile = "default"
			}
			region := strings.TrimSpace(mcpRegion)
			if region == "" {
				region = "global"
			}

			cfgDir, err := config.ResolveConfigDir(opts.ConfigDir)
			if err != nil {
				return err
			}

			// Prefer the Personal API pipeline for the read tools when a
			// key exists; fall back to OAuth.
			var c *client.Client
			cnRegion := config.Region(region).IsCN()
			if key, kerr := personalapi.LoadKey(cfgDir, cnRegion); kerr == nil && key != "" {
				if token, terr := personalapi.GetToken(context.Background(), cfgDir, cnRegion); terr == nil {
					c = client.NewWithSession(cfgDir, profile, &auth.Session{
						OAuth2: auth.OAuth2Token{
							TokenType:   "bearer",
							AccessToken: token,
							ExpiresAt:   time.Now().Add(6 * time.Hour).Unix(),
						},
					}, client.Options{BaseURL: personalGatewayBase(cnRegion), Region: config.Region(region)})
				}
			}
			if c == nil {
				var err error
				c, err = client.New(cfgDir, profile, client.Options{Region: config.Region(region)})
				if err != nil {
					return fmt.Errorf("MCP server: %w", err)
				}
			}

			srv := &mcpServer{client: c, profile: profile, region: region}
			srv.serve(os.Stdin, os.Stdout)
			return nil
		},
	}

	cmd.Flags().StringVar(&mcpProfile, "profile", "cn", "Garmin profile to use")
	cmd.Flags().StringVar(&mcpRegion, "region", "cn", "Garmin region: cn or global")
	return cmd
}

// ---- MCP server core ----

type mcpServer struct {
	client  *client.Client
	profile string
	region  string
}

func (s *mcpServer) serve(in io.Reader, out io.Writer) {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		resp := s.dispatch(&req)
		b, _ := json.Marshal(resp)
		fmt.Fprintln(out, string(b))
	}
}

func (s *mcpServer) dispatch(req *rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0"}
	if len(req.ID) > 0 {
		var id interface{}
		if json.Unmarshal(req.ID, &id) == nil {
			resp.ID = id
		}
	}

	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "garmin-sync", "version": "1.0.0"},
		}

	case "notifications/initialized":
		resp.ID = nil // notification: no response

	case "ping":
		resp.Result = map[string]any{}

	case "tools/list":
		tools := make([]map[string]any, 0)
		for _, t := range s.tools() {
			tools = append(tools, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": t.InputSchema,
			})
		}
		resp.Result = map[string]any{"tools": tools}

	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &rpcError{Code: -32700, Message: "parse error"}
			return resp
		}
		for _, t := range s.tools() {
			if t.Name == params.Name {
				result, err := t.Handler(context.Background(), params.Arguments)
				if err != nil {
					resp.Error = &rpcError{Code: -32000, Message: err.Error()}
					return resp
				}
				resp.Result = map[string]any{
					"content": []map[string]any{{"type": "text", "text": result}},
				}
				return resp
			}
		}
		resp.Error = &rpcError{Code: -32602, Message: fmt.Sprintf("unknown tool: %s", params.Name)}

	default:
		if len(req.ID) > 0 {
			resp.Error = &rpcError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)}
		} else {
			return rpcResponse{}
		}
	}
	return resp
}

// ---- tool registry ----

type mcpToolEntry struct {
	Name        string
	Description string
	InputSchema map[string]any
	Handler     func(ctx context.Context, args map[string]any) (string, error)
}

func (s *mcpServer) tools() []mcpToolEntry {
	c := s.client

	dateTool := func(name, desc string, fn func(ctx context.Context, date string) (any, error)) mcpToolEntry {
		return mcpToolEntry{
			Name: name, Description: desc,
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"date": map[string]any{"type": "string", "description": "Date (YYYY-MM-DD, default: today)"}},
			},
			Handler: func(ctx context.Context, args map[string]any) (string, error) {
				date := time.Now().Format("2006-01-02")
				if v, ok := args["date"].(string); ok && v != "" {
					date = v
				}
				result, err := fn(ctx, date)
				if err != nil {
					return "", err
				}
				b, _ := json.MarshalIndent(result, "", "  ")
				return string(b), nil
			},
		}
	}

	return []mcpToolEntry{
		dateTool("get_sleep", "Get sleep data (score, stages, SpO2, respiration)", func(ctx context.Context, date string) (any, error) {
			return garminhealth.GetSleep(ctx, c, date)
		}),
		dateTool("get_heart_rate", "Get resting/min/max heart rate for a date", func(ctx context.Context, date string) (any, error) {
			s, err := garminhealth.GetDailySummary(ctx, c, date)
			if err != nil {
				return nil, err
			}
			return map[string]any{"resting_hr": s.RestingHeartRate, "min_hr": s.MinHeartRate, "max_hr": s.MaxHeartRate}, nil
		}),
		dateTool("get_spo2", "Get SpO2 (pulse ox) data for a date", func(ctx context.Context, date string) (any, error) {
			return garminhealth.GetSpO2(ctx, c, date)
		}),
		dateTool("get_stress", "Get stress levels for a date (intraday timeline included)", func(ctx context.Context, date string) (any, error) {
			return garminhealth.GetStressDetail(ctx, c, date)
		}),
		dateTool("get_body_battery", "Get body battery (highest/lowest/most recent/charged/drained) for a date", func(ctx context.Context, date string) (any, error) {
			s, err := garminhealth.GetDailySummary(ctx, c, date)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"highest": s.BodyBatteryHighestValue, "lowest": s.BodyBatteryLowestValue,
				"most_recent": s.BodyBatteryMostRecentValue, "charged": s.BodyBatteryChargedValue,
			}, nil
		}),
		dateTool("get_daily_summary", "Get all-in-one daily summary (steps, HR, stress, body battery)", func(ctx context.Context, date string) (any, error) {
			return garminhealth.GetDailySummary(ctx, c, date)
		}),
		dateTool("get_training_readiness", "Get training readiness score and recovery metrics for a date", func(ctx context.Context, date string) (any, error) {
			return garmintraining.GetReadiness(ctx, c, date)
		}),
		dateTool("get_training_status", "Get training status (Productive, Peaking, etc.) for a date", func(ctx context.Context, date string) (any, error) {
			return garmintraining.GetStatus(ctx, c, date)
		}),
		dateTool("get_hrv", "Get HRV (heart rate variability) data for a date", func(ctx context.Context, date string) (any, error) {
			return garmintraining.GetHRV(ctx, c, date)
		}),
		{
			Name: "get_vo2max", Description: "Get VO2 max estimates (running and cycling)",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Handler: func(ctx context.Context, args map[string]any) (string, error) {
				v, err := garmintraining.GetVO2Max(ctx, c)
				if err != nil {
					return "", err
				}
				b, _ := json.Marshal(v)
				return string(b), nil
			},
		},
		{
			Name: "get_hr_zones", Description: "Get heart rate zone configuration",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"sport": map[string]any{"type": "string", "description": "Filter by sport (e.g. RUNNING)"},
			}},
			Handler: func(ctx context.Context, args map[string]any) (string, error) {
				sport := ""
				if v, ok := args["sport"].(string); ok {
					sport = v
				}
				zones, err := garmintraining.GetHRZones(ctx, c, sport)
				if err != nil {
					return "", err
				}
				b, _ := json.MarshalIndent(zones, "", "  ")
				return string(b), nil
			},
		},
		{
			Name: "get_lactate_threshold", Description: "Get lactate threshold HR and speed history (366-day max window)",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"days":  map[string]any{"type": "integer", "description": "Last N days (default 365)"},
				"sport": map[string]any{"type": "string", "description": "running or cycling"},
			}},
			Handler: func(ctx context.Context, args map[string]any) (string, error) {
				days := 365
				if v, ok := args["days"].(float64); ok {
					days = int(v)
				}
				sport := "RUNNING"
				if v, ok := args["sport"].(string); ok && v != "" {
					sport = strings.ToUpper(v)
				}
				to := time.Now().Format("2006-01-02")
				from := time.Now().AddDate(0, 0, -days+1).Format("2006-01-02")
				hr, err := garmintraining.LactateThresholdHeartRate(ctx, c, from, to, sport)
				if err != nil {
					return "", err
				}
				b, _ := json.MarshalIndent(hr, "", "  ")
				return string(b), nil
			},
		},
		{
			Name: "list_activities", Description: "List recent activities with summary metrics (id, name, type, date, distance, duration, calories, avg HR)",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"days":  map[string]any{"type": "integer", "description": "Last N days (default 30)"},
				"limit": map[string]any{"type": "integer", "description": "Max activities (default 20)"},
			}},
			Handler: func(ctx context.Context, args map[string]any) (string, error) {
				days := 30
				if v, ok := args["days"].(float64); ok {
					days = int(v)
				}
				limit := 20
				if v, ok := args["limit"].(float64); ok {
					limit = int(v)
				}
				from := time.Now().AddDate(0, 0, -days+1).Format("2006-01-02")
				to := time.Now().Format("2006-01-02")
				acts, err := garminactivities.List(ctx, c, limit, from, to, "")
				if err != nil {
					return "", err
				}
				b, _ := json.MarshalIndent(acts, "", "  ")
				return string(b), nil
			},
		},
		{
			Name: "get_activity", Description: "Get full detail for one activity (raw Garmin JSON)",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"activity_id": map[string]any{"type": "integer", "description": "Activity ID"},
			}},
			Handler: func(ctx context.Context, args map[string]any) (string, error) {
				id, _ := args["activity_id"].(float64)
				raw, err := garminactivities.GetRaw(ctx, c, int64(id))
				if err != nil {
					return "", err
				}
				b, _ := json.MarshalIndent(raw, "", "  ")
				return string(b), nil
			},
		},
		{
			Name: "get_activity_weather", Description: "Get weather recorded during an activity (temp °F, apparent temp, humidity, wind)",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"activity_id": map[string]any{"type": "integer", "description": "Activity ID"},
			}},
			Handler: func(ctx context.Context, args map[string]any) (string, error) {
				id, _ := args["activity_id"].(float64)
				w, err := garminactivities.GetWeather(ctx, c, int64(id))
				if err != nil {
					return "", err
				}
				b, _ := json.MarshalIndent(w, "", "  ")
				return string(b), nil
			},
		},
		{
			Name: "get_weight", Description: "Get weight and body composition data",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"days": map[string]any{"type": "integer", "description": "Last N days (default 30)"},
			}},
			Handler: func(ctx context.Context, args map[string]any) (string, error) {
				days := 30
				if v, ok := args["days"].(float64); ok {
					days = int(v)
				}
				from := time.Now().AddDate(0, 0, -days+1).Format("2006-01-02")
				to := time.Now().Format("2006-01-02")
				acts, err := garminweight.List(ctx, c, from, to)
				if err != nil {
					return "", err
				}
				b, _ := json.MarshalIndent(acts, "", "  ")
				return string(b), nil
			},
		},
	}
}

var _ = strconv.Itoa
