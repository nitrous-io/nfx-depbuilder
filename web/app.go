package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go/service/cloudwatchlogs/cloudwatchlogsiface"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/aws/aws-sdk-go/service/ecs/ecsiface"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/aws/aws-sdk-go/service/sqs/sqsiface"
	"github.com/nitrous-io/nfx-depbuilder/web/config"
	"github.com/nitrous-io/nfx-depbuilder/web/containerservice"
	"github.com/nitrous-io/nfx-depbuilder/web/filetransfer"
	"github.com/nitrous-io/nfx-depbuilder/web/logservice"
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

	presignExpiryDuration = 1 * time.Minute

	ecsStartedBy = "nfx-depbuilder-web"
)

var (
	S3                filetransfer.FileTransfer = filetransfer.NewS3()
	QueueSvc          sqsiface.SQSAPI
	ECSSvc            ecsiface.ECSAPI
	CloudWatchLogsSvc cloudwatchlogsiface.CloudWatchLogsAPI

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

	CloudWatchLogsSvc, err = logservice.New()
	if err != nil {
		log.Fatalf("failed to initialize CloudWatch Logs service: %v", err)
	}
}

func AppRouter() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.CloseNotify)

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})

	r.Route("/jobs", func(r chi.Router) {
		r.Use(AuthCtx)
		r.Post("/", createJob)

		r.Route("/:jobID", func(r chi.Router) {
			r.Use(JobCtx)
			r.Get("/", getJob)
			r.Get("/logs", jobLogs)
			r.Get("/result", jobResult)
		})
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

func getJob(w http.ResponseWriter, r *http.Request) {
	job := r.Context().Value("job").(*Job)

	render.Status(r, http.StatusOK)
	render.JSON(w, r, map[string]interface{}{
		"job": job,
	})
}

type logEvent struct {
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

// TODO This endpoint should probably support a streaming response either via
// HTTP long polling, Server-Sent Events or WebSockets.
// TODO When streaming response, also periodically poll the job state to see if
// it's "completed", then terminate the response stream.
func jobLogs(w http.ResponseWriter, r *http.Request) {
	job := r.Context().Value("job").(*Job)

	params := &cloudwatchlogs.GetLogEventsInput{
		LogGroupName:  aws.String(config.ResponseLogGroup),
		LogStreamName: aws.String(logStreamName(job.ID)),
	}

	if nextToken := r.FormValue("nextToken"); nextToken != "" {
		params.NextToken = aws.String(nextToken)
	}

	res, err := CloudWatchLogsSvc.GetLogEvents(params)
	if err != nil {
		renderFatalError(w, r, err)
		return
	}

	// Convert AWS SDK events to our log type.
	logs := make([]logEvent, 0, len(res.Events))
	for _, evt := range res.Events {
		logs = append(logs, logEvent{Message: *evt.Message, Timestamp: *evt.Timestamp})
	}

	render.Status(r, http.StatusOK)
	render.JSON(w, r, map[string]interface{}{
		"logs":       logs,
		"next_token": res.NextForwardToken,
	})
}

func jobResult(w http.ResponseWriter, r *http.Request) {
	job := r.Context().Value("job").(*Job)

	if job.State == stateFailed {
		render.Status(r, http.StatusNotFound)
		render.JSON(w, r, map[string]string{
			"error":             "not_found",
			"error_description": "job has failed",
		})
		return
	}

	if job.State != stateCompleted {
		render.Status(r, http.StatusNotFound)
		render.JSON(w, r, map[string]string{
			"error":             "not_found",
			"error_description": "job has not yet completed",
		})
		return
	}

	rgn := config.AWSRegion
	bkt := config.S3Bucket

	rKey := resultKey(job.ID)

	exists, err := S3.Exists(rgn, bkt, rKey)
	if err != nil {
		renderFatalError(w, r, err)
		return
	}
	if !exists {
		render.Status(r, http.StatusInternalServerError)
		render.JSON(w, r, map[string]string{
			"error":             "internal_server_error",
			"error_description": "job has completed but result is unavailable",
		})
		return
	}

	url, err := S3.PresignedURL(rgn, bkt, rKey, presignExpiryDuration)
	if err != nil {
		renderFatalError(w, r, err)
		return
	}

	render.Status(r, http.StatusOK)

	// We manually respond with JSON instead of using Chi's render.JSON()
	// so that we can disable escaping of "&" characters in the pre-signed URL.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]interface{}{"url": url}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
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

func JobCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jobID := chi.URLParam(r, "jobID")
		sKey := statusKey(jobID)

		var statusBuf aws.WriteAtBuffer
		err := S3.Download(config.AWSRegion, config.S3Bucket, sKey, &statusBuf)
		if err != nil {
			if err == filetransfer.ErrNotFound {
				render.Status(r, http.StatusNotFound)
				render.JSON(w, r, map[string]string{
					"error":             "not_found",
					"error_description": "no such job",
				})
				return
			}
			renderFatalError(w, r, err)
			return
		}

		var job Job
		if err := json.Unmarshal(statusBuf.Bytes(), &job); err != nil {
			renderFatalError(w, r, err)
			return
		}

		ctx := context.WithValue(r.Context(), "job", &job)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func packageJSONKey(jobID string) string {
	return "jobs/" + jobID + "/package.json"
}

func statusKey(jobID string) string {
	return "jobs/" + jobID + "/status.json"
}

func resultKey(jobID string) string {
	return "jobs/" + jobID + "/artifact.zip"
}

func logStreamName(jobID string) string {
	return "npm-install-log/" + jobID
}
