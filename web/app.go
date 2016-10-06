package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/nitrous-io/nfx-depbuilder/web/config"
	"github.com/nitrous-io/nfx-depbuilder/web/filetransfer"
	"github.com/pressly/chi"
	"github.com/pressly/chi/middleware"
	"github.com/pressly/chi/render"
)

type State string

const (
	stateProcessing State = "processing"
	stateFailed     State = "failed"
	stateCompleted  State = "completed"
)

type packageJSON struct {
	Dependencies map[string]string `json:"dependencies"`
}

const (
	maxUploadSize = int64(1024 * 1024 * 10) // 10 MiB
)

var (
	S3 filetransfer.FileTransfer = filetransfer.NewS3()

	errUploadFailed = errors.New("failed to upload")
)

func AppRouter() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.CloseNotify)

	r.Route("/jobs", func(r chi.Router) {
		r.Post("/", createJob)
	})

	return r
}

func createJob(w http.ResponseWriter, r *http.Request) {
	// Limit upload size (mostly to prevent abuse).
	if n, err := strconv.ParseInt(r.Header.Get("Content-Length"), 10, 64); err != nil || n > maxUploadSize {
		if err != nil {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, map[string]string{
				"error":             "bad_request",
				"error_description": "Content-Length header is required",
			})
		} else {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, map[string]string{
				"error":             "bad_request",
				"error_description": "request body is too large",
			})
		}
		return
	}

	mr, err := r.MultipartReader()
	if err != nil {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, map[string]string{
			"error":             "bad_request",
			"error_description": "request should be multipart/form-data",
		})
		return
	}

	var pkgRdr io.Reader

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			render.Status(r, http.StatusUnprocessableEntity)
			render.JSON(w, r, map[string]string{
				"error":             "invalid_params",
				"error_description": "a multipart section with the field name of 'file' is required",
			})
			return
		}
		if err != nil {
			renderFatalError(w, r, err)
			return
		}

		if part.FormName() != "file" {
			continue
		}

		pkgRdr = part

		break
	}

	var buf bytes.Buffer
	teeRdr := io.TeeReader(pkgRdr, &buf)

	var pkg packageJSON
	if err := json.NewDecoder(teeRdr).Decode(&pkg); err != nil {
		render.Status(r, http.StatusUnprocessableEntity)
		render.JSON(w, r, map[string]string{
			"error":             "invalid_params",
			"error_description": "uploaded file is not valid JSON",
		})
		return
	}

	// Sanity check that uploaded package.json contains a non-empty
	// "dependencies" field.
	if pkg.Dependencies == nil || len(pkg.Dependencies) == 0 {
		render.NoContent(w, r)
		return
	}

	jobID, err := randomID(8)
	if err != nil {
		renderFatalError(w, r, err)
		return
	}

	// Upload package.json to S3.
	s3key := "jobs/" + jobID + "/package.json"
	if err := S3.Upload(config.S3Region, config.S3Bucket, s3key, &buf, "", "private"); err != nil {
		log.Printf("failed to upload to S3, err: %v", err)
		renderFatalError(w, r, errUploadFailed)
		return
	}

	render.Status(r, http.StatusAccepted)
	render.JSON(w, r, map[string]interface{}{
		"job": map[string]interface{}{
			"id":    jobID,
			"state": stateProcessing,
		},
	})
}

func renderFatalError(w http.ResponseWriter, r *http.Request, err error) {
	// TODO Log the error with the request ID to a file (or a writer so we can
	// replace it with something that logs to an AWS service).
	log.Printf("fatal error: %v", err)

	reqID := middleware.GetReqID(r.Context())

	render.Status(r, http.StatusInternalServerError)
	render.JSON(w, r, map[string]string{
		"error":             "internal_server_error",
		"error_description": "an unexpected error has occurred",
		"help":              "your error has been logged, please contact support with your request_id",
		"request_id":        reqID,
	})
}
