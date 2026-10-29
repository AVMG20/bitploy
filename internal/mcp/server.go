// Package mcp is the MCP door: tools, resources, prompts and instructions.
// OWNER: MCP agent.
package mcp

import (
	"net/http"

	"github.com/arno/bitploy/internal/app"
)

// Handler returns the Streamable HTTP MCP endpoint (stateless), wrapped in
// bearer-token auth that resolves tokens through svc.AuthenticateToken.
func Handler(svc *app.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "MCP not implemented yet", http.StatusNotImplemented)
	})
}
