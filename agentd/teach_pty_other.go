//go:build !linux && !darwin

package agentd

import "net/http"

func (s *Server) teachPTY(w http.ResponseWriter, _ *http.Request) {
	s.fail(w, "teach", &httpError{status: http.StatusNotImplemented, msg: "this computer has no terminal for teaching"})
}
