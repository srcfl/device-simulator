package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
)

type Server struct {
	mux     http.Handler
	tools   []Tool
	toolMap map[string]ToolSpec
}

type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"inputSchema,omitempty"`
}

type ToolSpec struct {
	Tool         Tool
	Method       string
	PathTemplate string
	PathParams   []string
	QueryParams  []string
	BodyParams   []string
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type toolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// NewServer creates a new MCP server bound to the provided HTTP mux.
func NewServer(mux http.Handler) *Server {
	tools, toolMap := buildToolSpecs()
	return &Server{
		mux:     mux,
		tools:   tools,
		toolMap: toolMap,
	}
}

// HandleMCP handles MCP JSON-RPC requests (streamable HTTP).
func (s *Server) HandleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeResponse(w, r, rpcResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: -32700, Message: "Failed to read request body"},
		})
		return
	}

	// Support single request (batch can be added later if needed)
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.writeResponse(w, r, rpcResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: -32700, Message: "Invalid JSON-RPC request"},
		})
		return
	}

	resp := s.handleRPC(req)
	s.writeResponse(w, r, resp)
}

// HandleDiscovery serves MCP discovery information.
func (s *Server) HandleDiscovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"name":    "device-simulator",
		"version": "dev",
		"endpoint": map[string]interface{}{
			"transport": "http",
			"path":      "/mcp",
			"mode":      "streamable",
		},
	})
}

func (s *Server) handleRPC(req rpcRequest) rpcResponse {
	if req.JSONRPC != "2.0" {
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32600, Message: "Invalid JSON-RPC version"},
		}
	}

	switch req.Method {
	case "initialize":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"serverInfo": map[string]interface{}{
					"name":    "device-simulator",
					"version": "dev",
				},
				"capabilities": map[string]interface{}{
					"tools": map[string]interface{}{},
				},
			},
		}
	case "ping":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{}}
	case "tools/list":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"tools": s.tools,
			},
		}
	case "tools/call":
		var params toolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &rpcError{Code: -32602, Message: "Invalid tool call params"},
			}
		}
		result, err := s.callTool(params)
		if err != nil {
			return rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &rpcError{Code: -32603, Message: err.Error()},
			}
		}
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
	default:
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: "Method not found"},
		}
	}
}

func (s *Server) callTool(params toolCallParams) (map[string]interface{}, error) {
	if params.Name == "api_request" {
		return s.callAPIRequest(params.Arguments)
	}

	spec, ok := s.toolMap[params.Name]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", params.Name)
	}

	args := params.Arguments
	path := spec.PathTemplate
	for _, param := range spec.PathParams {
		val, ok := args[param]
		if !ok {
			return nil, fmt.Errorf("missing path parameter: %s", param)
		}
		path = strings.ReplaceAll(path, "{"+param+"}", url.PathEscape(fmt.Sprint(val)))
	}

	query := map[string]string{}
	for _, param := range spec.QueryParams {
		if val, ok := args[param]; ok {
			query[param] = fmt.Sprint(val)
		}
	}

	var body interface{}
	if bodyRaw, ok := args["body"]; ok {
		body = bodyRaw
	} else if len(spec.BodyParams) > 0 {
		payload := map[string]interface{}{}
		for _, param := range spec.BodyParams {
			if val, ok := args[param]; ok {
				payload[param] = val
			}
		}
		if len(payload) > 0 {
			body = payload
		}
	}

	apiResp, err := s.invokeAPI(spec.Method, path, query, body)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "json",
				"json": apiResp,
			},
		},
	}, nil
}

func (s *Server) callAPIRequest(args map[string]interface{}) (map[string]interface{}, error) {
	methodVal, ok := args["method"]
	if !ok {
		return nil, fmt.Errorf("missing method")
	}
	pathVal, ok := args["path"]
	if !ok {
		return nil, fmt.Errorf("missing path")
	}

	method := strings.ToUpper(fmt.Sprint(methodVal))
	path := fmt.Sprint(pathVal)

	query := map[string]string{}
	if queryRaw, ok := args["query"].(map[string]interface{}); ok {
		for key, value := range queryRaw {
			query[key] = fmt.Sprint(value)
		}
	}

	var body interface{}
	if bodyRaw, ok := args["body"]; ok {
		body = bodyRaw
	}

	apiResp, err := s.invokeAPI(method, path, query, body)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "json",
				"json": apiResp,
			},
		},
	}, nil
}

func (s *Server) invokeAPI(method, path string, query map[string]string, body interface{}) (map[string]interface{}, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	if len(query) > 0 {
		values := url.Values{}
		for key, value := range query {
			values.Set(key, value)
		}
		path = path + "?" + values.Encode()
	}

	var bodyReader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to encode request body: %w", err)
		}
		bodyReader = bytes.NewReader(payload)
	}

	req := httptest.NewRequest(method, path, bodyReader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp := httptest.NewRecorder()
	s.mux.ServeHTTP(resp, req)

	responseBody := resp.Body.Bytes()
	var parsed interface{}
	if len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, &parsed); err != nil {
			parsed = string(responseBody)
		}
	}

	return map[string]interface{}{
		"status": resp.Code,
		"ok":     resp.Code >= 200 && resp.Code < 400,
		"body":   parsed,
	}, nil
}

func (s *Server) writeResponse(w http.ResponseWriter, r *http.Request, resp rpcResponse) {
	payload, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	if isStreamRequest(r) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		fmt.Fprintf(w, "data: %s\n\n", payload)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(payload)
}

func isStreamRequest(r *http.Request) bool {
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		return true
	}
	return strings.EqualFold(r.URL.Query().Get("stream"), "true")
}

func buildToolSpecs() ([]Tool, map[string]ToolSpec) {
	specs := []ToolSpec{
		{
			Tool: Tool{
				Name:        "simulators.list",
				Description: "List all simulators",
				InputSchema: schemaObject(nil, nil),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/simulators",
		},
		{
			Tool: Tool{
				Name:        "simulators.create",
				Description: "Create a simulator",
				InputSchema: schemaObject(map[string]interface{}{
					"type": map[string]interface{}{
						"type":        "string",
						"description": "Simulator type (inverter, energy_meter, heat_pump, v2x_charger, ocpp_charger)",
					},
				}, []string{"type"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/simulators",
			BodyParams:   []string{"type"},
		},
		{
			Tool: Tool{
				Name:        "simulators.get",
				Description: "Get simulator details",
				InputSchema: schemaObject(map[string]interface{}{
					"id": schemaInteger("Simulator ID"),
				}, []string{"id"}),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/simulators/{id}",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "simulators.delete",
				Description: "Delete a simulator",
				InputSchema: schemaObject(map[string]interface{}{
					"id": schemaInteger("Simulator ID"),
				}, []string{"id"}),
			},
			Method:       http.MethodDelete,
			PathTemplate: "/api/simulators/{id}",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "simulators.update_config",
				Description: "Update simulator config",
				InputSchema: schemaObject(map[string]interface{}{
					"id":                   schemaInteger("Simulator ID"),
					"slave_id":             schemaInteger("Modbus slave ID"),
					"serial":               schemaString("Simulator serial"),
					"ocpp_url":             schemaString("OCPP URL"),
					"battery_capacity_kwh": schemaNumber("Battery capacity in kWh"),
				}, []string{"id"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/simulators/{id}/config",
			PathParams:   []string{"id"},
			BodyParams:   []string{"slave_id", "serial", "ocpp_url", "battery_capacity_kwh"},
		},
		{
			Tool: Tool{
				Name:        "simulators.registers.list",
				Description: "List all registers for a simulator",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Simulator ID")}, []string{"id"}),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/simulators/{id}/registers",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "simulators.registers.get",
				Description: "Get a simulator register",
				InputSchema: schemaObject(map[string]interface{}{
					"id":   schemaInteger("Simulator ID"),
					"addr": schemaInteger("Register address"),
				}, []string{"id", "addr"}),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/simulators/{id}/registers/{addr}",
			PathParams:   []string{"id", "addr"},
		},
		{
			Tool: Tool{
				Name:        "simulators.registers.write",
				Description: "Write a simulator register",
				InputSchema: schemaObject(map[string]interface{}{
					"id":    schemaInteger("Simulator ID"),
					"addr":  schemaInteger("Register address"),
					"value": schemaNumber("Register value"),
				}, []string{"id", "addr", "value"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/simulators/{id}/registers/{addr}",
			PathParams:   []string{"id", "addr"},
			BodyParams:   []string{"value"},
		},
		{
			Tool: Tool{
				Name:        "simulators.logs",
				Description: "Get simulator logs",
				InputSchema: schemaObject(map[string]interface{}{
					"id":    schemaInteger("Simulator ID"),
					"limit": schemaInteger("Max log entries"),
				}, []string{"id"}),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/simulators/{id}/logs",
			PathParams:   []string{"id"},
			QueryParams:  []string{"limit"},
		},
		{
			Tool: Tool{
				Name:        "simulators.profiles.list",
				Description: "List profiles for a simulator",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Simulator ID")}, []string{"id"}),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/simulators/{id}/profiles",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "simulators.profiles.apply",
				Description: "Apply a profile to a simulator",
				InputSchema: schemaObject(map[string]interface{}{
					"id":   schemaInteger("Simulator ID"),
					"name": schemaString("Profile name"),
				}, []string{"id", "name"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/simulators/{id}/profiles/{name}/apply",
			PathParams:   []string{"id", "name"},
		},
		{
			Tool: Tool{
				Name:        "simulators.automation.get",
				Description: "Get automation status",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Simulator ID")}, []string{"id"}),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/simulators/{id}/automation",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "simulators.automation.start",
				Description: "Start simulator automation",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Simulator ID")}, []string{"id"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/simulators/{id}/automation/start",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "simulators.automation.stop",
				Description: "Stop simulator automation",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Simulator ID")}, []string{"id"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/simulators/{id}/automation/stop",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "simulators.automation.config",
				Description: "Configure simulator automation",
				InputSchema: schemaObject(map[string]interface{}{
					"id":                 schemaInteger("Simulator ID"),
					"time_multiplier":    schemaNumber("Time multiplier"),
					"scenario":           schemaString("Scenario"),
					"profile":            schemaString("Profile"),
					"update_interval_ms": schemaInteger("Update interval ms"),
				}, []string{"id"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/simulators/{id}/automation/config",
			PathParams:   []string{"id"},
			BodyParams:   []string{"time_multiplier", "scenario", "profile", "update_interval_ms"},
		},
		{
			Tool: Tool{
				Name:        "simulators.start",
				Description: "Start simulator server",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Simulator ID")}, []string{"id"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/simulators/{id}/start",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "simulators.stop",
				Description: "Stop simulator server",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Simulator ID")}, []string{"id"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/simulators/{id}/stop",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "simulators.simulation.start",
				Description: "Start simulator simulation",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Simulator ID")}, []string{"id"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/simulators/{id}/simulation/start",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "simulators.simulation.stop",
				Description: "Stop simulator simulation",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Simulator ID")}, []string{"id"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/simulators/{id}/simulation/stop",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "simulators.state.update",
				Description: "Update simulator state",
				InputSchema: schemaObject(map[string]interface{}{
					"id":   schemaInteger("Simulator ID"),
					"body": schemaObject(nil, nil),
				}, []string{"id", "body"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/simulators/{id}/state",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "simulators.clients.list",
				Description: "List simulator clients",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Simulator ID")}, []string{"id"}),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/simulators/{id}/clients",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "simulators.wakeup",
				Description: "Wake up simulator",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Simulator ID")}, []string{"id"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/simulators/{id}/wakeup",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "simulators.power.update",
				Description: "Update simulator power",
				InputSchema: schemaObject(map[string]interface{}{
					"id":   schemaInteger("Simulator ID"),
					"body": schemaObject(nil, nil),
				}, []string{"id", "body"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/simulators/{id}/power",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "simulators.debug.meter_values",
				Description: "Get simulator debug meter values",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Simulator ID")}, []string{"id"}),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/simulators/{id}/debug/meter-values",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "simulators.reset",
				Description: "Reset simulator to defaults",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Simulator ID")}, []string{"id"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/simulators/{id}/reset",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "sites.list",
				Description: "List sites",
				InputSchema: schemaObject(nil, nil),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/sites",
		},
		{
			Tool: Tool{
				Name:        "sites.create",
				Description: "Create a site",
				InputSchema: schemaObject(map[string]interface{}{
					"name":          schemaString("Site name"),
					"meter_id":      schemaInteger("Meter simulator ID"),
					"simulator_ids": schemaArray("Simulator IDs", "integer"),
					"base_load_w":   schemaNumber("Base load in watts"),
				}, []string{"name"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/sites",
			BodyParams:   []string{"name", "meter_id", "simulator_ids", "base_load_w"},
		},
		{
			Tool: Tool{
				Name:        "sites.get",
				Description: "Get site details",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Site ID")}, []string{"id"}),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/sites/{id}",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "sites.delete",
				Description: "Delete a site",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Site ID")}, []string{"id"}),
			},
			Method:       http.MethodDelete,
			PathTemplate: "/api/sites/{id}",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "sites.update_config",
				Description: "Update site config",
				InputSchema: schemaObject(map[string]interface{}{
					"id":            schemaInteger("Site ID"),
					"name":          schemaString("Site name"),
					"base_load_w":   schemaNumber("Base load in watts"),
					"fuse_size":     schemaInteger("Fuse size"),
					"fuse_profile":  schemaString("Fuse profile"),
					"phase_factors": schemaObject(nil, nil),
					"fuse_blown":    schemaObject(nil, nil),
				}, []string{"id"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/sites/{id}/config",
			PathParams:   []string{"id"},
			BodyParams:   []string{"name", "base_load_w", "fuse_size", "fuse_profile", "phase_factors", "fuse_blown"},
		},
		{
			Tool: Tool{
				Name:        "sites.set_meter",
				Description: "Set site meter",
				InputSchema: schemaObject(map[string]interface{}{
					"id":       schemaInteger("Site ID"),
					"meter_id": schemaInteger("Meter simulator ID"),
				}, []string{"id", "meter_id"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/sites/{id}/meter",
			PathParams:   []string{"id"},
			BodyParams:   []string{"meter_id"},
		},
		{
			Tool: Tool{
				Name:        "sites.simulators.add",
				Description: "Add simulator to site",
				InputSchema: schemaObject(map[string]interface{}{
					"id":           schemaInteger("Site ID"),
					"simulator_id": schemaInteger("Simulator ID"),
				}, []string{"id", "simulator_id"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/sites/{id}/simulators",
			PathParams:   []string{"id"},
			BodyParams:   []string{"simulator_id"},
		},
		{
			Tool: Tool{
				Name:        "sites.simulators.remove",
				Description: "Remove simulator from site",
				InputSchema: schemaObject(map[string]interface{}{
					"id":     schemaInteger("Site ID"),
					"sim_id": schemaInteger("Simulator ID"),
				}, []string{"id", "sim_id"}),
			},
			Method:       http.MethodDelete,
			PathTemplate: "/api/sites/{id}/simulators/{sim_id}",
			PathParams:   []string{"id", "sim_id"},
		},
		{
			Tool: Tool{
				Name:        "sites.simulation.start",
				Description: "Start site simulation",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Site ID")}, []string{"id"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/sites/{id}/simulation/start",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "sites.simulation.stop",
				Description: "Stop site simulation",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Site ID")}, []string{"id"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/sites/{id}/simulation/stop",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "sites.simulation.config",
				Description: "Configure site simulation",
				InputSchema: schemaObject(map[string]interface{}{
					"id":              schemaInteger("Site ID"),
					"time_multiplier": schemaNumber("Time multiplier"),
					"scenario":        schemaString("Scenario"),
				}, []string{"id"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/sites/{id}/simulation/config",
			PathParams:   []string{"id"},
			BodyParams:   []string{"time_multiplier", "scenario"},
		},
		{
			Tool: Tool{
				Name:        "sites.device_power",
				Description: "Set device power state in site",
				InputSchema: schemaObject(map[string]interface{}{
					"id":           schemaInteger("Site ID"),
					"simulator_id": schemaInteger("Simulator ID"),
					"device_on":    schemaBoolean("Device on"),
				}, []string{"id", "simulator_id", "device_on"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/sites/{id}/device-power",
			PathParams:   []string{"id"},
			BodyParams:   []string{"simulator_id", "device_on"},
		},
		{
			Tool: Tool{
				Name:        "sites.meters.list",
				Description: "List site meters",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Site ID")}, []string{"id"}),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/sites/{id}/meters",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "sites.meters.add",
				Description: "Add a meter to site",
				InputSchema: schemaObject(map[string]interface{}{
					"id":       schemaInteger("Site ID"),
					"meter_id": schemaInteger("Meter simulator ID"),
				}, []string{"id", "meter_id"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/sites/{id}/meters",
			PathParams:   []string{"id"},
			BodyParams:   []string{"meter_id"},
		},
		{
			Tool: Tool{
				Name:        "sites.p1_mode",
				Description: "Set site P1 mode",
				InputSchema: schemaObject(map[string]interface{}{
					"id":   schemaInteger("Site ID"),
					"mode": schemaString("Mode"),
				}, []string{"id", "mode"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/sites/{id}/p1-mode",
			PathParams:   []string{"id"},
			BodyParams:   []string{"mode"},
		},
		{
			Tool: Tool{
				Name:        "sites.virtual_devices.list",
				Description: "List virtual devices",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Site ID")}, []string{"id"}),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/sites/{id}/virtual-devices",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "sites.virtual_devices.write",
				Description: "Write to a virtual device",
				InputSchema: schemaObject(map[string]interface{}{
					"id":   schemaInteger("Site ID"),
					"type": schemaString("Device type"),
					"body": schemaObject(nil, nil),
				}, []string{"id", "type", "body"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/sites/{id}/virtual-devices/{type}",
			PathParams:   []string{"id", "type"},
		},
		{
			Tool: Tool{
				Name:        "sites.phase_loads.update",
				Description: "Update site phase loads",
				InputSchema: schemaObject(map[string]interface{}{
					"id":     schemaInteger("Site ID"),
					"l1":     schemaNumber("Phase 1 load"),
					"l2":     schemaNumber("Phase 2 load"),
					"l3":     schemaNumber("Phase 3 load"),
					"linked": schemaBoolean("Linked phases"),
				}, []string{"id"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/sites/{id}/phase-loads",
			PathParams:   []string{"id"},
			BodyParams:   []string{"l1", "l2", "l3", "linked"},
		},
		{
			Tool: Tool{
				Name:        "sites.load_scenario.update",
				Description: "Set site load scenario",
				InputSchema: schemaObject(map[string]interface{}{
					"id":       schemaInteger("Site ID"),
					"scenario": schemaString("Scenario key"),
				}, []string{"id", "scenario"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/sites/{id}/load-scenario",
			PathParams:   []string{"id"},
			BodyParams:   []string{"scenario"},
		},
		{
			Tool: Tool{
				Name:        "load_scenarios.list",
				Description: "List load scenarios",
				InputSchema: schemaObject(nil, nil),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/load-scenarios",
		},
		{
			Tool: Tool{
				Name:        "facilities.list",
				Description: "List facility presets (house ASHP/GSHP, apartment, PV+battery)",
				InputSchema: schemaObject(nil, nil),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/facilities",
		},
		{
			Tool: Tool{
				Name:        "sites.facility.get",
				Description: "Get the facility preset applied to a site",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Site ID")}, []string{"id"}),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/sites/{id}/facility",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "sites.facility.apply",
				Description: "Apply a facility preset: plant physics plus inverter and heat-pump Modbus facades",
				InputSchema: schemaObject(map[string]interface{}{
					"id":     schemaInteger("Site ID"),
					"preset": schemaString("Preset id: house-ashp, house-gshp, apartment-ashp, pv-battery"),
					"start":  schemaBoolean("Start site simulation (default true)"),
				}, []string{"id", "preset"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/sites/{id}/facility",
			PathParams:   []string{"id"},
			BodyParams:   []string{"preset", "start"},
		},
		{
			Tool: Tool{
				Name:        "sites.plant.get",
				Description: "Get the live site-plant snapshot (electrical + thermal + schematic flows)",
				InputSchema: schemaObject(map[string]interface{}{"id": schemaInteger("Site ID")}, []string{"id"}),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/sites/{id}/plant",
			PathParams:   []string{"id"},
		},
		{
			Tool: Tool{
				Name:        "system.info",
				Description: "Get system info",
				InputSchema: schemaObject(nil, nil),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/system",
		},
		{
			Tool: Tool{
				Name:        "system.version",
				Description: "Get system version",
				InputSchema: schemaObject(nil, nil),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/version",
		},
		{
			Tool: Tool{
				Name:        "settings.get",
				Description: "Get runtime settings",
				InputSchema: schemaObject(nil, nil),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/settings",
		},
		{
			Tool: Tool{
				Name:        "settings.update",
				Description: "Update runtime settings",
				InputSchema: schemaObject(map[string]interface{}{
					"body": schemaObject(nil, nil),
				}, []string{"body"}),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/settings",
		},
		{
			Tool: Tool{
				Name:        "system.restart",
				Description: "Restart the application",
				InputSchema: schemaObject(nil, nil),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/restart",
		},
		{
			Tool: Tool{
				Name:        "warnings.list",
				Description: "List warnings",
				InputSchema: schemaObject(nil, nil),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/warnings",
		},
		{
			Tool: Tool{
				Name:        "warnings.clear",
				Description: "Clear warnings",
				InputSchema: schemaObject(nil, nil),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/warnings/clear",
		},
		{
			Tool: Tool{
				Name:        "updates.check",
				Description: "Check for updates",
				InputSchema: schemaObject(nil, nil),
			},
			Method:       http.MethodGet,
			PathTemplate: "/api/update/check",
		},
		{
			Tool: Tool{
				Name:        "updates.apply",
				Description: "Apply update",
				InputSchema: schemaObject(nil, nil),
			},
			Method:       http.MethodPost,
			PathTemplate: "/api/update/apply",
		},
		{
			Tool: Tool{
				Name:        "health.check",
				Description: "Health check",
				InputSchema: schemaObject(nil, nil),
			},
			Method:       http.MethodGet,
			PathTemplate: "/health",
		},
	}

	tools := make([]Tool, 0, len(specs)+1)
	toolMap := make(map[string]ToolSpec, len(specs)+1)
	for _, spec := range specs {
		tools = append(tools, spec.Tool)
		toolMap[spec.Tool.Name] = spec
	}

	tools = append(tools, Tool{
		Name:        "api_request",
		Description: "Perform an arbitrary API request (method/path/query/body)",
		InputSchema: schemaObject(map[string]interface{}{
			"method": schemaString("HTTP method"),
			"path":   schemaString("Request path"),
			"query":  schemaObject(nil, nil),
			"body":   schemaObject(nil, nil),
		}, []string{"method", "path"}),
	})

	return tools, toolMap
}

func schemaObject(properties map[string]interface{}, required []string) map[string]interface{} {
	schema := map[string]interface{}{
		"type": "object",
	}
	if properties != nil {
		schema["properties"] = properties
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func schemaString(description string) map[string]interface{} {
	schema := map[string]interface{}{"type": "string"}
	if description != "" {
		schema["description"] = description
	}
	return schema
}

func schemaInteger(description string) map[string]interface{} {
	schema := map[string]interface{}{"type": "integer"}
	if description != "" {
		schema["description"] = description
	}
	return schema
}

func schemaNumber(description string) map[string]interface{} {
	schema := map[string]interface{}{"type": "number"}
	if description != "" {
		schema["description"] = description
	}
	return schema
}

func schemaBoolean(description string) map[string]interface{} {
	schema := map[string]interface{}{"type": "boolean"}
	if description != "" {
		schema["description"] = description
	}
	return schema
}

func schemaArray(description string, itemType string) map[string]interface{} {
	schema := map[string]interface{}{
		"type":  "array",
		"items": map[string]interface{}{"type": itemType},
	}
	if description != "" {
		schema["description"] = description
	}
	return schema
}
