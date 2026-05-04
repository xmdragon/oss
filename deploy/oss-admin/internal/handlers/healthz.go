package handlers

import "net/http"

// GetHealthz is a tiny endpoint for monitoring. Returns 200 if MinIO is
// reachable, 503 otherwise. No auth — bound to loopback so external monitors
// hit it via Caddy with whatever ACL the operator configures upstream.
func (s *Server) GetHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	if err := s.MC.Healthy(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("minio unreachable: " + err.Error()))
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}
