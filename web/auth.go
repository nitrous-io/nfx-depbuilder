package web

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/nitrous-io/nfx-depbuilder/web/config"
	"github.com/pressly/chi/render"
)

var bearerTokenAuthHeaderRe = regexp.MustCompile(`\A\s*Bearer\s+([\S]+)\s*\z`)

func AuthCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Requires a verified Nitrous API access token in the request.

		authHeader := r.Header.Get("Authorization")

		match := bearerTokenAuthHeaderRe.FindStringSubmatch(authHeader)
		if match == nil || len(match) < 1 {
			r.Header.Add("WWW-Authenticate", `Bearer realm="nitrous"`)
			render.Status(r, http.StatusUnauthorized)
			render.JSON(w, r, map[string]string{
				"error":             "invalid_token",
				"error_description": "a Nitrous access token is required",
			})
			return
		}

		token := match[1]
		req, err := http.NewRequest("GET", config.NitrousHost+"/api/v0/user", nil)
		if err != nil {
			renderFatalError(w, r, err)
			return
		}

		req.Header.Set("Authorization", "Bearer "+token)

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			renderFatalError(w, r, err)
			return
		}

		if res.StatusCode != http.StatusOK {
			r.Header.Add("WWW-Authenticate", `Bearer realm="nitrous"`)
			render.Status(r, http.StatusUnauthorized)
			render.JSON(w, r, map[string]string{
				"error":             "invalid_token",
				"error_description": "Nitrous access token is invalid",
			})
			return
		}

		var j struct {
			User struct {
				Username string `json:"username"`
			} `json:"user"`
		}
		if err := json.NewDecoder(res.Body).Decode(&j); err != nil {
			r.Header.Add("WWW-Authenticate", `Bearer realm="nitrous"`)
			render.Status(r, http.StatusUnauthorized)
			render.JSON(w, r, map[string]string{
				"error":             "invalid_token",
				"error_description": "Nitrous access token is invalid",
			})
			return
		}

		ctx := context.WithValue(r.Context(), "nitrousUsername", j.User.Username)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
