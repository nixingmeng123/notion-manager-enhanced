package proxy

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// GatewayError deliberately contains no upstream body, URL, cookies or headers.
// In particular a 403 is a capability rejection, not proof of an invalid login.
type GatewayError struct {
	Kind      string
	Status    int
	Message   string
	Retryable bool
}

func (e *GatewayError) Error() string { return e.Message }

func gatewayError(kind string, status int, message string, retry bool) *GatewayError {
	return &GatewayError{Kind: kind, Status: status, Message: message, Retryable: retry}
}

func asGatewayError(err error) *GatewayError {
	var result *GatewayError
	if errors.As(err, &result) {
		return result
	}
	if errors.Is(err, context.Canceled) {
		return gatewayError("cancelled", 499, "request cancelled", false)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return gatewayError("timeout", http.StatusGatewayTimeout, "Agent request timed out before completion", false)
	}
	return gatewayError("upstream_error", http.StatusBadGateway, "Agent upstream request failed", true)
}

type inferenceRoute struct{ Protocol, Model string }

func resolveInferenceRoute(model string) (inferenceRoute, error) {
	protocol := enhancedConfigSnapshot().DefaultProtocol
	name := strings.TrimSpace(model)
	if strings.Contains(name, "/") {
		parts := strings.SplitN(name, "/", 2)
		protocol, name = parts[0], parts[1]
	}
	if protocol == "" {
		protocol = "workflow"
	}
	if name == "" {
		return inferenceRoute{}, gatewayError("unknown_model", 404, "model name is required", false)
	}
	switch protocol {
	case "agent":
		if _, ok := lookupAgentModel(name); !ok {
			return inferenceRoute{}, gatewayError("unknown_model", 404, "unknown Agent model", false)
		}
	case "workflow":
		resolved := ResolveModel(name)
		known := IsResearcherModel(name)
		for _, slug := range SnapshotModelMap() {
			if slug == resolved {
				known = true
				break
			}
		}
		if !known {
			return inferenceRoute{}, gatewayError("unknown_model", 404, "unknown Workflow model", false)
		}
	default:
		return inferenceRoute{}, gatewayError("unsupported_protocol", 400, "supported model prefixes are agent/ and workflow/", false)
	}
	return inferenceRoute{Protocol: protocol, Model: name}, nil
}

// CallInference is the compatible entry point for both backends.
func CallInference(acc *Account, messages []ChatMessage, model string, disableBuiltinTools bool, cb StreamCallback, opts ...CallOptions) error {
	var opt CallOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	name, ask := StripAskModeSuffix(model)
	if opt.IsResearcher {
		opt.Protocol = "workflow"
	}
	if opt.Protocol != "" && !strings.Contains(name, "/") {
		name = opt.Protocol + "/" + name
	}
	route, err := resolveInferenceRoute(name)
	if err != nil {
		return err
	}
	opt.UseReadOnlyMode = opt.UseReadOnlyMode || ask
	if route.Protocol == "agent" {
		return CallAgentInference(acc, messages, route.Model, disableBuiltinTools, cb, opt)
	}
	return CallWorkflowInference(acc, messages, route.Model, disableBuiltinTools, cb, opt)
}
