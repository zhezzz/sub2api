package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

const (
	ImageTaskModeGeneration = "generation"
	ImageTaskModeEdit       = "edit"

	ImageTaskStatusPending   = "pending"
	ImageTaskStatusRunning   = "running"
	ImageTaskStatusSucceeded = "succeeded"
	ImageTaskStatusFailed    = "failed"
)

const (
	imageTaskWorkerCount     = 4
	imageTaskQueueSize       = 50
	imageTaskTimeout         = 20 * time.Minute
	imageTaskRetention       = 24 * time.Hour
	imageTaskCleanupInterval = time.Hour
)

var (
	ErrImageTaskNotFound  = infraerrors.NotFound("IMAGE_TASK_NOT_FOUND", "image task not found")
	ErrImageTaskQueueFull = infraerrors.TooManyRequests("IMAGE_TASK_QUEUE_FULL", "image task queue is full, please retry later")
)

type ImageTaskRepository interface {
	Create(ctx context.Context, task *ImageTask) error
	GetByID(ctx context.Context, id int64) (*ImageTask, error)
	GetByIDForUser(ctx context.Context, userID, id int64) (*ImageTask, error)
	ListByUser(ctx context.Context, userID int64, params pagination.PaginationParams) ([]ImageTask, *pagination.PaginationResult, error)
	MarkRunning(ctx context.Context, id int64, startedAt time.Time) error
	MarkSucceeded(ctx context.Context, id int64, responseJSON string, finishedAt time.Time) error
	MarkFailed(ctx context.Context, id int64, errorMessage string, finishedAt time.Time) error
	FailStale(ctx context.Context, cutoff time.Time, errorMessage string) (int, error)
	DeleteFinishedBefore(ctx context.Context, cutoff time.Time) (int, error)
}

type ImageTask struct {
	ID              int64      `json:"id"`
	UserID          int64      `json:"user_id"`
	Mode            string     `json:"mode"`
	Status          string     `json:"status"`
	Model           string     `json:"model"`
	Prompt          string     `json:"prompt"`
	Size            string     `json:"size"`
	N               int        `json:"n"`
	Quality         string     `json:"quality"`
	RequestJSON     string     `json:"request_json,omitempty"`
	ResponseJSON    string     `json:"response_json,omitempty"`
	ErrorMessage    string     `json:"error_message,omitempty"`
	InputImagesJSON string     `json:"input_images_json,omitempty"`
	InputMaskJSON   string     `json:"input_mask_json,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type ImageTaskUpload struct {
	FieldName   string `json:"field_name"`
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	DataBase64  string `json:"data_base64"`
}

type CreateImageTaskInput struct {
	UserID    int64
	APIKey    string
	Mode      string
	Model     string
	Prompt    string
	Size      string
	N         int
	Quality   string
	Images    []ImageTaskUpload
	Mask      *ImageTaskUpload
	RawFields map[string]any
}

type ImageTaskService struct {
	repo   ImageTaskRepository
	cfg    *config.Config
	client *http.Client
	jobs   chan imageTaskJob
	slots  chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type imageTaskJob struct {
	taskID int64
	apiKey string
}

func NewImageTaskService(repo ImageTaskRepository, cfg *config.Config) *ImageTaskService {
	ctx, cancel := context.WithCancel(context.Background())
	return &ImageTaskService{
		repo:   repo,
		cfg:    cfg,
		client: &http.Client{Timeout: imageTaskTimeout},
		jobs:   make(chan imageTaskJob, imageTaskQueueSize),
		slots:  make(chan struct{}, imageTaskQueueSize),
		ctx:    ctx,
		cancel: cancel,
	}
}

func (s *ImageTaskService) Start() {
	ctx := context.Background()
	if n, err := s.repo.FailStale(ctx, time.Now().Add(time.Second), "task interrupted by service restart"); err != nil {
		logger.LegacyPrintf("service.image_task", "startup stale task cleanup failed: %v", err)
	} else if n > 0 {
		logger.LegacyPrintf("service.image_task", "marked %d stale tasks as failed", n)
	}
	for i := 0; i < imageTaskWorkerCount; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	s.wg.Add(1)
	go s.cleanupLoop()
}

func (s *ImageTaskService) Stop() {
	s.cancel()
	s.wg.Wait()
}

func (s *ImageTaskService) Create(ctx context.Context, input CreateImageTaskInput) (*ImageTask, error) {
	if err := validateImageTaskInput(input); err != nil {
		return nil, err
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return nil, ErrImageTaskQueueFull
	}

	task, err := s.createTask(ctx, input)
	if err != nil {
		<-s.slots
		return nil, err
	}
	select {
	case s.jobs <- imageTaskJob{taskID: task.ID, apiKey: input.APIKey}:
		return task, nil
	case <-s.ctx.Done():
		<-s.slots
		return nil, infraerrors.InternalServer("IMAGE_TASK_SERVICE_STOPPED", "image task service is stopping")
	}
}

func (s *ImageTaskService) Get(ctx context.Context, userID, id int64) (*ImageTask, error) {
	task, err := s.repo.GetByIDForUser(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	return task, nil
}

func (s *ImageTaskService) List(ctx context.Context, userID int64, params pagination.PaginationParams) ([]ImageTask, *pagination.PaginationResult, error) {
	return s.repo.ListByUser(ctx, userID, params)
}

func (s *ImageTaskService) createTask(ctx context.Context, input CreateImageTaskInput) (*ImageTask, error) {
	requestJSON, err := json.Marshal(input.RawFields)
	if err != nil {
		return nil, err
	}
	inputImagesJSON, err := json.Marshal(input.Images)
	if err != nil {
		return nil, err
	}
	inputMaskJSON := []byte("")
	if input.Mask != nil {
		inputMaskJSON, err = json.Marshal(input.Mask)
		if err != nil {
			return nil, err
		}
	}

	task := &ImageTask{
		UserID:          input.UserID,
		Mode:            input.Mode,
		Status:          ImageTaskStatusPending,
		Model:           input.Model,
		Prompt:          input.Prompt,
		Size:            input.Size,
		N:               normalizedImageTaskN(input.N),
		Quality:         input.Quality,
		RequestJSON:     string(requestJSON),
		InputImagesJSON: string(inputImagesJSON),
		InputMaskJSON:   string(inputMaskJSON),
	}
	if err := s.repo.Create(ctx, task); err != nil {
		return nil, err
	}
	return task, nil
}

func validateImageTaskInput(input CreateImageTaskInput) error {
	switch input.Mode {
	case ImageTaskModeGeneration, ImageTaskModeEdit:
	default:
		return infraerrors.BadRequest("INVALID_IMAGE_TASK_MODE", "invalid image task mode")
	}
	if input.UserID <= 0 || strings.TrimSpace(input.APIKey) == "" {
		return infraerrors.BadRequest("INVALID_IMAGE_TASK_REQUEST", "missing user or api key")
	}
	if strings.TrimSpace(input.Model) == "" {
		return infraerrors.BadRequest("INVALID_IMAGE_TASK_REQUEST", "model is required")
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return infraerrors.BadRequest("INVALID_IMAGE_TASK_REQUEST", "prompt is required")
	}
	if input.Mode == ImageTaskModeEdit && len(input.Images) == 0 {
		return infraerrors.BadRequest("INVALID_IMAGE_TASK_REQUEST", "at least one image is required")
	}
	return nil
}

func normalizedImageTaskN(n int) int {
	if n <= 0 {
		return 1
	}
	return n
}

func (s *ImageTaskService) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case job := <-s.jobs:
			s.runTask(job)
			<-s.slots
		}
	}
}

func (s *ImageTaskService) cleanupLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(imageTaskCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			if _, err := s.repo.FailStale(ctx, time.Now().Add(-imageTaskTimeout), "task timed out"); err != nil {
				logger.LegacyPrintf("service.image_task", "stale task cleanup failed: %v", err)
			}
			if _, err := s.repo.DeleteFinishedBefore(ctx, time.Now().Add(-imageTaskRetention)); err != nil {
				logger.LegacyPrintf("service.image_task", "finished task cleanup failed: %v", err)
			}
			cancel()
		}
	}
}

func (s *ImageTaskService) runTask(job imageTaskJob) {
	ctx, cancel := context.WithTimeout(s.ctx, imageTaskTimeout)
	defer cancel()

	task, err := s.repo.GetByID(ctx, job.taskID)
	if err != nil {
		logger.LegacyPrintf("service.image_task", "load task %d failed: %v", job.taskID, err)
		return
	}
	now := time.Now()
	if err := s.repo.MarkRunning(ctx, task.ID, now); err != nil {
		logger.LegacyPrintf("service.image_task", "mark task %d running failed: %v", job.taskID, err)
		return
	}

	if err := s.executeTask(ctx, task, job.apiKey); err != nil {
		_ = s.repo.MarkFailed(context.Background(), task.ID, truncateImageTaskError(err.Error()), time.Now())
	}
}

func (s *ImageTaskService) executeTask(ctx context.Context, task *ImageTask, apiKey string) error {
	req, err := s.buildNativeRequest(ctx, task, apiKey)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("native images endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return s.repo.MarkSucceeded(context.Background(), task.ID, string(body), time.Now())
}

func (s *ImageTaskService) buildNativeRequest(ctx context.Context, task *ImageTask, apiKey string) (*http.Request, error) {
	url := s.nativeImagesURL(task.Mode)
	var body io.Reader
	contentType := "application/json"
	if task.Mode == ImageTaskModeGeneration {
		payload := map[string]any{
			"model":           task.Model,
			"prompt":          task.Prompt,
			"n":               task.N,
			"stream":          false,
			"response_format": "b64_json",
		}
		setOptionalString(payload, "size", task.Size)
		setOptionalString(payload, "quality", task.Quality)
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	} else {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		writeMultipartField(writer, "model", task.Model)
		writeMultipartField(writer, "prompt", task.Prompt)
		writeMultipartField(writer, "n", strconv.Itoa(task.N))
		writeMultipartField(writer, "stream", "false")
		writeMultipartField(writer, "response_format", "b64_json")
		writeMultipartField(writer, "size", task.Size)
		writeMultipartField(writer, "quality", task.Quality)
		if err := writeImageTaskUploads(writer, task.InputImagesJSON); err != nil {
			return nil, err
		}
		if err := writeImageTaskMask(writer, task.InputMaskJSON); err != nil {
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		contentType = writer.FormDataContentType()
		body = &buf
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", contentType)
	return req, nil
}

func (s *ImageTaskService) nativeImagesURL(mode string) string {
	host := "127.0.0.1"
	port := 3000
	if s.cfg != nil {
		port = s.cfg.Server.Port
		if h := strings.TrimSpace(s.cfg.Server.Host); h != "" && h != "0.0.0.0" && h != "::" {
			host = h
		}
	}
	endpoint := "/v1/images/generations"
	if mode == ImageTaskModeEdit {
		endpoint = "/v1/images/edits"
	}
	return fmt.Sprintf("http://%s:%d%s", host, port, endpoint)
}

func setOptionalString(payload map[string]any, key, value string) {
	if strings.TrimSpace(value) != "" {
		payload[key] = value
	}
}

func writeMultipartField(writer *multipart.Writer, key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	_ = writer.WriteField(key, value)
}

func writeImageTaskUploads(writer *multipart.Writer, raw string) error {
	var uploads []ImageTaskUpload
	if err := json.Unmarshal([]byte(raw), &uploads); err != nil {
		return err
	}
	for _, upload := range uploads {
		fieldName := upload.FieldName
		if strings.TrimSpace(fieldName) == "" {
			fieldName = "image"
		}
		if err := writeImageTaskUpload(writer, fieldName, upload); err != nil {
			return err
		}
	}
	return nil
}

func writeImageTaskMask(writer *multipart.Writer, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var upload ImageTaskUpload
	if err := json.Unmarshal([]byte(raw), &upload); err != nil {
		return err
	}
	return writeImageTaskUpload(writer, "mask", upload)
}

func writeImageTaskUpload(writer *multipart.Writer, fieldName string, upload ImageTaskUpload) error {
	data, err := decodeImageTaskUpload(upload)
	if err != nil {
		return err
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, escapeMultipartQuote(fieldName), escapeMultipartQuote(upload.FileName)))
	if upload.ContentType != "" {
		header.Set("Content-Type", upload.ContentType)
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	_, err = part.Write(data)
	return err
}

func decodeImageTaskUpload(upload ImageTaskUpload) ([]byte, error) {
	return base64.StdEncoding.DecodeString(upload.DataBase64)
}

func escapeMultipartQuote(s string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, "\\\"").Replace(s)
}

func truncateImageTaskError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 4000 {
		return message[:4000]
	}
	return message
}
