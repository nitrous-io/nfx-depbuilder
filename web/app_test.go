package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"testing"

	"github.com/aws/aws-sdk-go/service/cloudwatchlogs"
	"github.com/nitrous-io/nfx-depbuilder/web/config"
	"github.com/nitrous-io/nfx-depbuilder/web/containerservice"
	"github.com/nitrous-io/nfx-depbuilder/web/filetransfer"
	"github.com/nitrous-io/nfx-depbuilder/web/filetransfer/fake"
	"github.com/nitrous-io/nfx-depbuilder/web/logservice"
	"github.com/nitrous-io/nfx-depbuilder/web/queue"
)

func TestMain(m *testing.M) {
	config.AWSRegion = "kl-orgrimmar-1"
	config.S3Bucket = "nfx-jobs"
	config.SQSQueueURL = "https://sqs.kl-orgrimmar-1.amazonaws.test/12345/a-queue"
	config.TaskDefinitionARN = "arn:aws:ecs:kl-orgrimmar-1:123:task-definition/taskdef:1"
	config.TaskClusterName = "fake-cluster"

	os.Exit(m.Run())
}

func matchJSON(t *testing.T, res *http.Response, expected string) {
	b := &bytes.Buffer{}
	if _, err := b.ReadFrom(res.Body); err != nil {
		t.Fatalf("error reading response body: %v", err)
	}

	var x, y map[string]interface{}

	if err := json.Unmarshal(b.Bytes(), &x); err != nil {
		t.Logf("response body: %q", b.String())
		t.Fatalf("response body %q is probably invalid JSON, err: %v",
			b.String(), err)
	}

	if err := json.Unmarshal([]byte(expected), &y); err != nil {
		t.Fatalf("expected string %q is probably invalid JSON, err: %v",
			expected, err)
	}

	if !reflect.DeepEqual(x, y) {
		t.Logf("response body: %q", b.String())
		t.Logf("expected: %q", expected)
		t.Errorf("unexpected response body")
	}
}

func doMultipartRequest(t *testing.T, u string, uploadBody string) *http.Response {
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)

	var (
		fieldname = "file"
		filename  = "package.json"
	)
	part, err := mw.CreateFormFile(fieldname, filename)
	if err != nil {
		t.Fatalf("error creating multipart section: %v", err)
	}

	fileBody := bytes.NewBufferString(uploadBody)
	if _, err := io.Copy(part, fileBody); err != nil {
		t.Fatalf("error copying file body into multipart section: %v", err)
	}

	if err := mw.Close(); err != nil {
		t.Fatalf("error closing multipart writer: %v", err)
	}

	req, err := http.NewRequest("POST", u, body)
	if err != nil {
		t.Fatalf("error creating multipart request: %v", err)
	}

	req.Header.Set("Content-Type", mw.FormDataContentType())

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("error making multipart request: %v", err)
	}

	return res
}

func TestCreateJobEmptyBody(t *testing.T) {
	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	req, err := http.NewRequest("POST", ts.URL+"/jobs", nil)
	if err != nil {
		t.Fatalf("error creating POST request: %v", err)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("error making multipart request: %v", err)
	}

	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected response status to be %v but got %v",
			http.StatusBadRequest, res.StatusCode)
	}

	expectedJSON := `{
		"error": "bad_request",
		"error_description": "request should be multipart/form-data"
	}`
	matchJSON(t, res, expectedJSON)
}

func TestCreateJobNotMultipartRequest(t *testing.T) {
	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	res, err := http.PostForm(ts.URL+"/jobs",
		url.Values{"file": {validPackageJSON}})
	if err != nil {
		t.Fatalf("error making POST request: %v", err)
	}

	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status to be %v but got %v",
			http.StatusBadRequest, res.StatusCode)
	}

	expectedJSON := `{
		"error": "bad_request",
		"error_description": "request should be multipart/form-data"
	}`
	matchJSON(t, res, expectedJSON)
}

func TestCreateJobUploadTooLarge(t *testing.T) {
	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	res, err := http.PostForm(ts.URL+"/jobs",
		url.Values{"file": {largeBody}})
	if err != nil {
		t.Fatalf("error making POST request: %v", err)
	}

	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status to be %v but got %v",
			http.StatusBadRequest, res.StatusCode)
	}

	expectedJSON := `{
		"error": "bad_request",
		"error_description": "request body is too large"
	}`
	matchJSON(t, res, expectedJSON)
}

func TestCreateJobMissingContentLength(t *testing.T) {
	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)

	var (
		fieldname = "file"
		filename  = "package.json"
	)
	part, err := mw.CreateFormFile(fieldname, filename)
	if err != nil {
		t.Fatalf("error creating multipart section: %v", err)
	}

	fileBody := bytes.NewBufferString(validPackageJSON)
	if _, err := io.Copy(part, fileBody); err != nil {
		t.Fatalf("error copying file body into multipart section: %v", err)
	}

	if err := mw.Close(); err != nil {
		t.Fatalf("error closing multipart writer: %v", err)
	}

	req, err := http.NewRequest("POST", ts.URL+"/jobs", body)
	if err != nil {
		t.Fatalf("error creating multipart request: %v", err)
	}

	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.ContentLength = -1

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("error making multipart request: %v", err)
	}

	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected response status to be %v but got %v",
			http.StatusBadRequest, res.StatusCode)
	}

	expectedJSON := `{
		"error": "bad_request",
		"error_description": "Content-Length header is required"
	}`
	matchJSON(t, res, expectedJSON)
}

func TestCreateJobMultipartFormNameIncorrect(t *testing.T) {
	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)

	var (
		fieldname = "incorrectname"
		filename  = "package.json"
	)
	part, err := mw.CreateFormFile(fieldname, filename)
	if err != nil {
		t.Fatalf("error creating multipart section: %v", err)
	}

	fileBody := bytes.NewBufferString(validPackageJSON)
	if _, err := io.Copy(part, fileBody); err != nil {
		t.Fatalf("error copying file body into multipart section: %v", err)
	}

	if err := mw.Close(); err != nil {
		t.Fatalf("error closing multipart writer: %v", err)
	}

	req, err := http.NewRequest("POST", ts.URL+"/jobs", body)
	if err != nil {
		t.Fatalf("error creating multipart request: %v", err)
	}

	req.Header.Set("Content-Type", mw.FormDataContentType())

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("error making multipart request: %v", err)
	}

	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected response status to be %v but got %v",
			http.StatusUnprocessableEntity, res.StatusCode)
	}

	expectedJSON := `{
		"error": "invalid_params",
		"error_description": "a multipart section with the field name of 'file' is required"
	}`
	matchJSON(t, res, expectedJSON)
}

func TestCreateJobInvalidJSON(t *testing.T) {
	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	res := doMultipartRequest(t, ts.URL+"/jobs", invalidPackageJSON)

	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected response status to be %v but got %v",
			http.StatusUnprocessableEntity, res.StatusCode)
	}

	expectedJSON := `{
		"error": "invalid_params",
		"error_description": "uploaded file is not valid JSON"
	}`
	matchJSON(t, res, expectedJSON)
}

func TestCreateJobNoDependencies(t *testing.T) {
	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	res := doMultipartRequest(t, ts.URL+"/jobs", noDependenciesPackageJSON)

	if res.StatusCode != http.StatusNoContent {
		t.Errorf("expected response status to be %v but got %v",
			http.StatusNoContent, res.StatusCode)
	}
}

func TestCreateJobAccepted(t *testing.T) {
	// Fake S3 upload so that tests run without actually trying to upload.
	origS3 := S3
	fakeS3 := &fake.FakeTransfer{}
	S3 = fakeS3
	defer func() { S3 = origS3 }()

	// Fake queue service.
	origQueueSvc := QueueSvc
	fakeQueueSvc := &queue.Fake{}
	QueueSvc = fakeQueueSvc
	defer func() { QueueSvc = origQueueSvc }()

	// Fake ECS service.
	origECSSvc := ECSSvc
	fakeECSSvc := &containerservice.Fake{}
	ECSSvc = fakeECSSvc
	defer func() { ECSSvc = origECSSvc }()

	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	res := doMultipartRequest(t, ts.URL+"/jobs", validPackageJSON)

	if res.StatusCode != http.StatusAccepted {
		t.Errorf("expected response status to be %v but got %v",
			http.StatusAccepted, res.StatusCode)
	}

	b := &bytes.Buffer{}
	if _, err := b.ReadFrom(res.Body); err != nil {
		t.Fatalf("error reading response body: %v", err)
	}

	var j struct {
		Job Job `json:"job"`
	}
	if err := json.Unmarshal(b.Bytes(), &j); err != nil {
		t.Logf("response body: %q", b.String())
		t.Fatalf("response body %q is probably invalid JSON, err: %v",
			b.String(), err)
	}

	job := j.Job

	matched, err := regexp.MatchString("[0-9a-z]{8}", job.ID)
	if err != nil {
		t.Fatalf("unexpected error matching against regex: %v", err)
	}
	if !matched {
		t.Errorf("expected job ID to consist of 8 alphanumeric characters, but got %v", job.ID)
	}

	if job.State != "processing" {
		t.Errorf("expected job to have a state or processing but got %q", job.State)
	}
}

func TestCreateJobUploadsToS3(t *testing.T) {
	// Fake S3 upload so that tests run without actually trying to upload.
	origS3 := S3
	fakeS3 := &fake.FakeTransfer{}
	S3 = fakeS3
	defer func() { S3 = origS3 }()

	// Fake queue service.
	origQueueSvc := QueueSvc
	fakeQueueSvc := &queue.Fake{}
	QueueSvc = fakeQueueSvc
	defer func() { QueueSvc = origQueueSvc }()

	// Fake ECS service.
	origECSSvc := ECSSvc
	fakeECSSvc := &containerservice.Fake{}
	ECSSvc = fakeECSSvc
	defer func() { ECSSvc = origECSSvc }()

	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	res := doMultipartRequest(t, ts.URL+"/jobs", validPackageJSON)

	if res.StatusCode != http.StatusAccepted {
		t.Errorf("expected response status to be %v but got %v",
			http.StatusAccepted, res.StatusCode)
	}

	b := &bytes.Buffer{}
	if _, err := b.ReadFrom(res.Body); err != nil {
		t.Fatalf("error reading response body: %v", err)
	}

	var j struct {
		Job struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"job"`
	}
	if err := json.Unmarshal(b.Bytes(), &j); err != nil {
		t.Logf("response body: %q", b.String())
		t.Fatalf("response body %q is probably invalid JSON, err: %v",
			b.String(), err)
	}

	if fakeS3.UploadCalls.Count() != 2 {
		t.Fatalf("expected S3.Upload() to be called 2 times but was called %d times", fakeS3.UploadCalls.Count())
	}

	call1 := fakeS3.UploadCalls.NthCall(1)
	args1 := call1.Arguments

	if args1[0] != config.AWSRegion {
		t.Fatalf("expected S3.Upload() to be called with %v for region, but got %v", config.AWSRegion, args1[0])
	}
	if args1[1] != config.S3Bucket {
		t.Fatalf("expected S3.Upload() to be called with %v for bucket, but got %v", config.S3Bucket, args1[1])
	}
	expectedKey := fmt.Sprintf("jobs/%v/package.json", j.Job.ID)
	if args1[2] != expectedKey {
		t.Fatalf("expected S3.Upload() to be called with S3 key of %q, but got %q", expectedKey, args1[2])
	}
	if args1[4] != "" {
		t.Fatalf("expected S3.Upload() to be called without content type, but got %q", args1[4])
	}
	if args1[5] != "private" {
		t.Fatalf("expected S3.Upload() to be called with an ACL of 'private', but got %q", args1[5])
	}

	uploadedContent := string(call1.SideEffects["uploaded_content"].([]byte))
	if uploadedContent != validPackageJSON {
		t.Logf("expected content: %q", validPackageJSON)
		t.Logf("uploaded content: %q", uploadedContent)
		t.Fatalf("expected S3.Upload() to upload the given content but it did not")
	}

	call2 := fakeS3.UploadCalls.NthCall(2)
	args2 := call2.Arguments

	if args2[0] != config.AWSRegion {
		t.Fatalf("expected S3.Upload() to be called with %v for region, but got %v", config.AWSRegion, args2[0])
	}
	if args2[1] != config.S3Bucket {
		t.Fatalf("expected S3.Upload() to be called with %v for bucket, but got %v", config.S3Bucket, args2[1])
	}
	expectedKey = fmt.Sprintf("jobs/%v/status.json", j.Job.ID)
	if args2[2] != expectedKey {
		t.Fatalf("expected S3.Upload() to be called with S3 key of %q, but got %q", expectedKey, args2[2])
	}
	if args2[4] != "" {
		t.Fatalf("expected S3.Upload() to be called without content type, but got %q", args2[4])
	}
	if args2[5] != "private" {
		t.Fatalf("expected S3.Upload() to be called with an ACL of 'private', but got %q", args2[5])
	}

	statusBytes := call2.SideEffects["uploaded_content"].([]byte)
	var receivedJob Job
	if err := json.Unmarshal(statusBytes, &receivedJob); err != nil {
		t.Fatalf("error unmarshalling status.json, err: %v", err)
	}

	expectedJob := Job{ID: j.Job.ID, State: stateProcessing}
	if receivedJob != expectedJob {
		t.Fatalf("expected S3.Upload() to upload the job %#v but it got %#v instead", expectedJob, receivedJob)
	}
}

func TestCreateJobSendsSQSMessage(t *testing.T) {
	// Fake S3 upload so that tests run without actually trying to upload.
	origS3 := S3
	fakeS3 := &fake.FakeTransfer{}
	S3 = fakeS3
	defer func() { S3 = origS3 }()

	// Fake queue service.
	origQueueSvc := QueueSvc
	fakeQueueSvc := &queue.Fake{}
	QueueSvc = fakeQueueSvc
	defer func() { QueueSvc = origQueueSvc }()

	// Fake ECS service.
	origECSSvc := ECSSvc
	fakeECSSvc := &containerservice.Fake{}
	ECSSvc = fakeECSSvc
	defer func() { ECSSvc = origECSSvc }()

	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	res := doMultipartRequest(t, ts.URL+"/jobs", validPackageJSON)

	if res.StatusCode != http.StatusAccepted {
		t.Errorf("expected response status to be %v but got %v",
			http.StatusAccepted, res.StatusCode)
	}

	b := &bytes.Buffer{}
	if _, err := b.ReadFrom(res.Body); err != nil {
		t.Fatalf("error reading response body: %v", err)
	}

	var j struct {
		Job struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"job"`
	}
	if err := json.Unmarshal(b.Bytes(), &j); err != nil {
		t.Logf("response body: %q", b.String())
		t.Fatalf("response body %q is probably invalid JSON, err: %v",
			b.String(), err)
	}

	msgBody := *fakeQueueSvc.SendMessageInput.MessageBody

	var m struct {
		JobID  string `json:"job_id"`
		Bucket string `json:"bucket"`
		Key    string `json:"key"`
	}
	if err := json.Unmarshal([]byte(msgBody), &m); err != nil {
		t.Fatalf("error unmarshalling sent message %q, err: %v", msgBody, err)
	}

	if m.JobID != j.Job.ID {
		t.Errorf("expected message to have a job ID of %q but got %q", j.Job.ID, m.JobID)
	}

	expectedKey := fmt.Sprintf("jobs/%v/package.json", j.Job.ID)
	if m.Key != expectedKey {
		t.Errorf("expected message to have key of %q but got %q", expectedKey, m.Key)
	}

	if m.Bucket != config.S3Bucket {
		t.Errorf("expected message to have bucket of %q, but got %q", config.S3Bucket, m.Bucket)
	}
}

func TestCreateJobRunsECSTask(t *testing.T) {
	// Fake S3 upload so that tests run without actually trying to upload.
	origS3 := S3
	fakeS3 := &fake.FakeTransfer{}
	S3 = fakeS3
	defer func() { S3 = origS3 }()

	// Fake queue service.
	origQueueSvc := QueueSvc
	fakeQueueSvc := &queue.Fake{}
	QueueSvc = fakeQueueSvc
	defer func() { QueueSvc = origQueueSvc }()

	// Fake ECS service.
	origECSSvc := ECSSvc
	fakeECSSvc := &containerservice.Fake{}
	ECSSvc = fakeECSSvc
	defer func() { ECSSvc = origECSSvc }()

	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	if fakeECSSvc.RunTaskInput != nil {
		t.Fatalf("expected mock ECS service to not have .RunTaskInput but it was %v", fakeECSSvc.RunTaskInput)
	}

	res := doMultipartRequest(t, ts.URL+"/jobs", validPackageJSON)

	if res.StatusCode != http.StatusAccepted {
		t.Errorf("expected response status to be %v but got %v",
			http.StatusAccepted, res.StatusCode)
	}

	if fakeECSSvc.RunTaskInput == nil {
		t.Fatalf("expected mock ECS service to have its .RunTaskInput set, but it was nil")
	}
}

func TestGetJobDownloadsStatusFromS3(t *testing.T) {
	// Fake S3 download so that tests run without actually trying to download.
	origS3 := S3
	fakeS3 := &fake.FakeTransfer{}
	S3 = fakeS3
	defer func() { S3 = origS3 }()

	job := Job{ID: "abc123", State: stateProcessing}
	jobJSON, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("error JSON encoding job %#v, err: %v", job, err)
	}

	fakeS3.DownloadContent = jobJSON

	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/jobs/" + job.ID)
	if err != nil {
		t.Fatalf("error with GET request to endpoint: %v", err)
	}

	if res.StatusCode != http.StatusOK {
		t.Errorf("expected response status to be %v, but got %v",
			http.StatusOK, res.StatusCode)
	}

	b := &bytes.Buffer{}
	if _, err := b.ReadFrom(res.Body); err != nil {
		t.Fatalf("error reading response body: %v", err)
	}

	var j struct {
		Job Job `json:"job"`
	}
	if err := json.Unmarshal(b.Bytes(), &j); err != nil {
		t.Logf("response body: %q", b.String())
		t.Fatalf("response body %q is probably invalid JSON, err: %v",
			b.String(), err)
	}

	if j.Job != job {
		t.Errorf("expected response body to return job %#v, but got %#v",
			job, j.Job)
	}
}

func TestGetJobNotFound(t *testing.T) {
	// Fake S3 download so that tests run without actually trying to download.
	origS3 := S3
	fakeS3 := &fake.FakeTransfer{}
	S3 = fakeS3
	defer func() { S3 = origS3 }()

	fakeS3.DownloadError = filetransfer.ErrNotFound

	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/jobs/abc123")
	if err != nil {
		t.Fatalf("error with GET request to endpoint: %v", err)
	}

	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected response status to be %v, but got %v",
			http.StatusOK, res.StatusCode)
	}

	expectedJSON := `{
		"error": "not_found",
		"error_description": "no such job"
	}`
	matchJSON(t, res, expectedJSON)
}

func TestJobResult(t *testing.T) {
	// Fake S3 download so that tests run without actually trying to download.
	origS3 := S3
	fakeS3 := &fake.FakeTransfer{}
	S3 = fakeS3
	defer func() { S3 = origS3 }()

	job := Job{ID: "abc123", State: stateCompleted}
	jobJSON, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("error JSON encoding job %#v, err: %v", job, err)
	}

	fakeS3.DownloadContent = jobJSON
	fakeS3.ExistsReturn = true
	fakeS3.PresignedURLReturn = "https://s3-kl-orgrimmar-1.amazonaws.test/jobs/abc123/artifact.zip?foo=bar"

	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/jobs/" + job.ID + "/result")
	if err != nil {
		t.Fatalf("error with GET request to endpoint: %v", err)
	}

	if res.StatusCode != http.StatusOK {
		t.Errorf("expected response status to be %v, but got %v",
			http.StatusOK, res.StatusCode)
	}

	b := &bytes.Buffer{}
	if _, err := b.ReadFrom(res.Body); err != nil {
		t.Fatalf("error reading response body: %v", err)
	}

	var j struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(b.Bytes(), &j); err != nil {
		t.Logf("response body: %q", b.String())
		t.Fatalf("response body %q is probably invalid JSON, err: %v",
			b.String(), err)
	}

	if j.URL != fakeS3.PresignedURLReturn {
		t.Errorf("expected response body to return pre-signed URL of %q, but got %q",
			fakeS3.PresignedURLReturn, j.URL)
	}
}

func TestJobResultReturnsNotFoundIfJobFailed(t *testing.T) {
	// Fake S3 download so that tests run without actually trying to download.
	origS3 := S3
	fakeS3 := &fake.FakeTransfer{}
	S3 = fakeS3
	defer func() { S3 = origS3 }()

	job := Job{ID: "abc123", State: stateFailed} // Failed.
	jobJSON, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("error JSON encoding job %#v, err: %v", job, err)
	}

	fakeS3.DownloadContent = jobJSON

	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/jobs/" + job.ID + "/result")
	if err != nil {
		t.Fatalf("error with GET request to endpoint: %v", err)
	}

	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected response status to be %v, but got %v",
			http.StatusNotFound, res.StatusCode)
	}

	expectedJSON := `{
		"error": "not_found",
		"error_description": "job has failed"
	}`
	matchJSON(t, res, expectedJSON)
}

func TestJobResultReturnsNotFoundIfJobNotComplete(t *testing.T) {
	// Fake S3 download so that tests run without actually trying to download.
	origS3 := S3
	fakeS3 := &fake.FakeTransfer{}
	S3 = fakeS3
	defer func() { S3 = origS3 }()

	job := Job{ID: "abc123", State: stateProcessing} // Still "processing".
	jobJSON, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("error JSON encoding job %#v, err: %v", job, err)
	}

	fakeS3.DownloadContent = jobJSON

	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/jobs/" + job.ID + "/result")
	if err != nil {
		t.Fatalf("error with GET request to endpoint: %v", err)
	}

	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected response status to be %v, but got %v",
			http.StatusNotFound, res.StatusCode)
	}

	expectedJSON := `{
		"error": "not_found",
		"error_description": "job has not yet completed"
	}`
	matchJSON(t, res, expectedJSON)
}

func TestJobResultArtifactNotFound(t *testing.T) {
	// Fake S3 download so that tests run without actually trying to download.
	origS3 := S3
	fakeS3 := &fake.FakeTransfer{}
	S3 = fakeS3
	defer func() { S3 = origS3 }()

	job := Job{ID: "abc123", State: stateCompleted}
	jobJSON, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("error JSON encoding job %#v, err: %v", job, err)
	}

	fakeS3.DownloadContent = jobJSON
	fakeS3.ExistsReturn = false // Build artifact doesn't exist on S3.

	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/jobs/" + job.ID + "/result")
	if err != nil {
		t.Fatalf("error with GET request to endpoint: %v", err)
	}

	if res.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected response status to be %v, but got %v",
			http.StatusInternalServerError, res.StatusCode)
	}

	expectedJSON := `{
		"error": "internal_server_error",
		"error_description": "job has completed but result is unavailable"
	}`
	matchJSON(t, res, expectedJSON)
}

func TestJobLogsGetsLogsFromCloudWatchLogs(t *testing.T) {
	// Fake S3 download so that tests run without actually trying to download.
	origS3 := S3
	fakeS3 := &fake.FakeTransfer{}
	S3 = fakeS3
	defer func() { S3 = origS3 }()

	job := Job{ID: "abc123", State: stateProcessing}
	jobJSON, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("error JSON encoding job %#v, err: %v", job, err)
	}
	fakeS3.DownloadContent = jobJSON

	origCloudWatchLogsSvc := CloudWatchLogsSvc
	fakeCloudWatchLogsSvc := &logservice.Fake{}
	CloudWatchLogsSvc = fakeCloudWatchLogsSvc
	defer func() { CloudWatchLogsSvc = origCloudWatchLogsSvc }()

	var (
		e1IngestionTime = int64(1476091254847)
		e1Timestamp     = int64(1476091254464)
		e1Message       = "async@2.0.1 node_modules/async"
		e2IngestionTime = int64(1476091257864)
		e2Timestamp     = int64(1476091257345)
		e2Message       = "└── lodash@4.16.4"
		fwdToken        = "f/xyzforwardtoken"
		bckToken        = "b/zyxbackwardtoken"
	)
	fakeCloudWatchLogsSvc.GetLogEventsOutput = &cloudwatchlogs.GetLogEventsOutput{
		Events: []*cloudwatchlogs.OutputLogEvent{
			{
				IngestionTime: &e1IngestionTime,
				Timestamp:     &e1Timestamp,
				Message:       &e1Message,
			},
			{
				IngestionTime: &e2IngestionTime,
				Timestamp:     &e2Timestamp,
				Message:       &e2Message,
			},
		},
		NextForwardToken:  &fwdToken,
		NextBackwardToken: &bckToken,
	}

	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/jobs/" + job.ID + "/logs")
	if err != nil {
		t.Fatalf("error with GET request to endpoint: %v", err)
	}

	if res.StatusCode != http.StatusOK {
		t.Errorf("expected response status to be %v, but got %v",
			http.StatusOK, res.StatusCode)
	}

	b := &bytes.Buffer{}
	if _, err := b.ReadFrom(res.Body); err != nil {
		t.Fatalf("error reading response body: %v", err)
	}

	var j struct {
		Logs      []logEvent `json:"logs"`
		NextToken string     `json:"next_token"`
	}
	if err := json.Unmarshal(b.Bytes(), &j); err != nil {
		t.Logf("response body: %q", b.String())
		t.Fatalf("response body %q is probably invalid JSON, err: %v",
			b.String(), err)
	}

	if j.NextToken != fwdToken {
		t.Errorf("expected next_token to be %q but got %q",
			fwdToken, j.NextToken)
	}

	if len(j.Logs) != 2 {
		t.Errorf("expected 2 logs but got %d", len(j.Logs))
	}

	if j.Logs[0].Message != e1Message {
		t.Errorf("expected first log message to be %q but got %q",
			e1Message, j.Logs[0].Message)
	}

	if j.Logs[0].Timestamp != e1Timestamp {
		t.Errorf("expected first log timestamp to be %v but got %v",
			e1Timestamp, j.Logs[0].Timestamp)
	}

	if j.Logs[1].Message != e2Message {
		t.Errorf("expected second log message to be %q but got %q",
			e2Message, j.Logs[1].Message)
	}

	if j.Logs[1].Timestamp != e2Timestamp {
		t.Errorf("expected second log timestamp to be %v but got %v",
			e2Timestamp, j.Logs[1].Timestamp)
	}

	input := fakeCloudWatchLogsSvc.GetLogEventsInput
	if *input.LogGroupName != config.ResponseLogGroup {
		t.Errorf("expected log group name used to be %q but got %q",
			config.ResponseLogGroup, *input.LogGroupName)
	}
	expectedLogStreamName := "npm-install-log/" + job.ID
	if *input.LogStreamName != expectedLogStreamName {
		t.Errorf("expected log stream name used to be %q but got %q",
			expectedLogStreamName, *input.LogStreamName)
	}
}

func TestJobLogsNextTokenParam(t *testing.T) {
	// Fake S3 download so that tests run without actually trying to download.
	origS3 := S3
	fakeS3 := &fake.FakeTransfer{}
	S3 = fakeS3
	defer func() { S3 = origS3 }()

	job := Job{ID: "abc123", State: stateProcessing}
	jobJSON, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("error JSON encoding job %#v, err: %v", job, err)
	}
	fakeS3.DownloadContent = jobJSON

	origCloudWatchLogsSvc := CloudWatchLogsSvc
	fakeCloudWatchLogsSvc := &logservice.Fake{}
	CloudWatchLogsSvc = fakeCloudWatchLogsSvc
	defer func() { CloudWatchLogsSvc = origCloudWatchLogsSvc }()

	var (
		fwdToken = "f/xyzforwardtoken"
		bckToken = "b/zyxbackwardtoken"
	)
	fakeCloudWatchLogsSvc.GetLogEventsOutput = &cloudwatchlogs.GetLogEventsOutput{
		Events:            []*cloudwatchlogs.OutputLogEvent{},
		NextForwardToken:  &fwdToken,
		NextBackwardToken: &bckToken,
	}

	r := AppRouter()
	ts := httptest.NewServer(r)
	defer ts.Close()

	u, err := url.Parse(ts.URL + "/jobs/" + job.ID + "/logs")
	if err != nil {
		t.Fatalf("error parsing URL: %v", err)
	}

	params := url.Values{}
	nextToken := "some-token"
	params.Add("nextToken", nextToken)

	u.RawQuery = params.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		t.Fatalf("error creating request: %v", err)
	}

	if _, err := http.DefaultClient.Do(req); err != nil {
		t.Fatalf("error making multipart request: %v", err)
	}

	input := fakeCloudWatchLogsSvc.GetLogEventsInput
	if input == nil {
		t.Fatal("expected GetLogEvents() to be called with some input but it was nil")
	}
	if input.NextToken == nil {
		t.Fatalf("expected nextToken param to be %q but it was nil", nextToken)
	}
	if *input.NextToken != nextToken {
		t.Fatalf("expected nextToken param to be %q but it was %q",
			nextToken, *input.NextToken)
	}
}

const validPackageJSON = `{
  "name": "worker-launcher",
  "version": "1.0.0",
  "dependencies": {
	"async": "^2.0.1"
  },
  "devDependencies": {},
  "scripts": {},
  "author": "Nitrous Inc.",
  "license": "ISC"
}`

const invalidPackageJSON = `{
  invalidKey: "foo",
  "name": "worker-launcher",
  "version": "1.0.0",
  "dependencies": {
	"async": "^2.0.1"
  },
  "devDependencies": {},
  "scripts": {},
  "author": "Nitrous Inc.",
  "license": "ISC"
}`

const noDependenciesPackageJSON = `{
  "name": "worker-launcher",
  "version": "1.0.0",
  "dependencies": {},
  "devDependencies": {},
  "scripts": {},
  "author": "Nitrous Inc.",
  "license": "ISC"
}`

// Fake request body that's larger than the upload limit.
var largeBody = string(make([]byte, maxUploadSize+1))
