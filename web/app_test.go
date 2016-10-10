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

	"github.com/nitrous-io/nfx-depbuilder/web/config"
	"github.com/nitrous-io/nfx-depbuilder/web/containerservice"
	"github.com/nitrous-io/nfx-depbuilder/web/filetransfer/fake"
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
		Bucket string `json:"bucket"`
		Key    string `json:"key"`
	}
	if err := json.Unmarshal([]byte(msgBody), &m); err != nil {
		t.Fatalf("error unmarshalling sent message %q, err: %v", msgBody, err)
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
