package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/aws/aws-sdk-go/service/ecs/ecsiface"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/aws/aws-sdk-go/service/sqs/sqsiface"
	"github.com/nitrous-io/nfx-depbuilder/web/config"
	"github.com/nitrous-io/nfx-depbuilder/web/containerservice"
	"github.com/nitrous-io/nfx-depbuilder/web/filetransfer"
	"github.com/nitrous-io/nfx-depbuilder/web/queue"
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

type Job struct {
	ID    string `json:"id"`
	State State  `json:"state"`
}

type packageJSON struct {
	Dependencies map[string]string `json:"dependencies"`
}

const (
	maxUploadSize = int64(1024 * 1024 * 10) // 10 MiB

	ecsStartedBy = "nfx-depbuilder-web"
)

var (
	S3       filetransfer.FileTransfer = filetransfer.NewS3()
	QueueSvc sqsiface.SQSAPI
	ECSSvc   ecsiface.ECSAPI

	errUploadFailed = errors.New("failed to upload")
)

func init() {
	var err error
	QueueSvc, err = queue.New()
	if err != nil {
		log.Fatalf("failed to initialize queue: %v", err)
	}

	ECSSvc, err = containerservice.New()
	if err != nil {
		log.Fatalf("failed to initialize ECS service: %v", err)
	}
}

func AppRouter() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
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

	// FIXME Use a pipe so that we don't buffer the upload into program memory.
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

	// We might want to add the user ID to the jobID when we implement
	// authentication via Nitrous.
	jobID, err := randomID(8)
	if err != nil {
		renderFatalError(w, r, err)
		return
	}

	rgn := config.AWSRegion
	bkt := config.S3Bucket

	// Upload package.json to S3.
	pkgKey := packageJSONKey(jobID)
	if err := S3.Upload(rgn, bkt, pkgKey, &buf, "", "private"); err != nil {
		log.Printf("failed to upload package.json to S3, err: %v", err)
		renderFatalError(w, r, errUploadFailed)
		return
	}

	// Send message to SQS queue with S3 bucket + key.
	msg, err := json.Marshal(map[string]string{
		"job_id": jobID,
		"bucket": bkt,
		"key":    pkgKey,
	})
	if err != nil {
		renderFatalError(w, r, err)
		return
	}
	if _, err := QueueSvc.SendMessage(&sqs.SendMessageInput{
		MessageBody: aws.String(string(msg)),
		QueueUrl:    aws.String(config.SQSQueueURL),
	}); err != nil {
		renderFatalError(w, r, err)
		return
	}

	// Upload status file to S3. S3 kinda works but we can use something less
	// primitive than regular files, like DyanomoDB.
	sKey := statusKey(jobID)
	job := Job{
		ID:    jobID,
		State: stateProcessing,
	}
	statusJSON, err := json.Marshal(job)
	if err != nil {
		renderFatalError(w, r, err)
		return
	}
	if err := S3.Upload(rgn, bkt, sKey, bytes.NewReader(statusJSON), "", "private"); err != nil {
		log.Printf("failed to upload status.json to S3, err: %v", err)
		renderFatalError(w, r, errUploadFailed)
		return
	}

	// Run ECS task to handle the message we just sent to the queue.
	if res, err := ECSSvc.RunTask(&ecs.RunTaskInput{
		TaskDefinition: aws.String(config.TaskDefinitionARN),
		Cluster:        aws.String(config.TaskClusterName),
		Count:          aws.Int64(1),
		StartedBy:      aws.String(ecsStartedBy),
	}); err != nil {
		renderFatalError(w, r, err)
		return
	} else if res != nil && len(res.Failures) > 0 {
		// Log and continue - there will be external mechanisms to ensure that
		// ECS tasks are started to handle messages in the SQS queue.
		log.Printf("ECS RunTask has failure(s): %#v", res.Failures)
	}

	render.Status(r, http.StatusAccepted)
	render.JSON(w, r, map[string]interface{}{
		"job": job,
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

func packageJSONKey(jobID string) string {
	return "jobs/" + jobID + "/package.json"
}

func statusKey(jobID string) string {
	return "jobs/" + jobID + "/status.json"
}
