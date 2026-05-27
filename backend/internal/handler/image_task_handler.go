package handler

import (
	"encoding/base64"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	imageTaskMaxUploadSize  = 10 << 20
	imageTaskMaxImageCount  = 10
	imageTaskMaxRequestSize = 120 << 20
)

type ImageTaskHandler struct {
	imageTaskService *service.ImageTaskService
}

func NewImageTaskHandler(imageTaskService *service.ImageTaskService) *ImageTaskHandler {
	return &ImageTaskHandler{imageTaskService: imageTaskService}
}

type createImageGenerationTaskRequest struct {
	APIKey  string `json:"api_key" binding:"required"`
	Model   string `json:"model" binding:"required"`
	Prompt  string `json:"prompt" binding:"required"`
	Size    string `json:"size"`
	N       int    `json:"n"`
	Quality string `json:"quality"`
}

func (h *ImageTaskHandler) CreateGeneration(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var req createImageGenerationTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	task, err := h.imageTaskService.Create(c.Request.Context(), service.CreateImageTaskInput{
		UserID:  subject.UserID,
		APIKey:  strings.TrimSpace(req.APIKey),
		Mode:    service.ImageTaskModeGeneration,
		Model:   strings.TrimSpace(req.Model),
		Prompt:  strings.TrimSpace(req.Prompt),
		Size:    strings.TrimSpace(req.Size),
		N:       req.N,
		Quality: strings.TrimSpace(req.Quality),
		RawFields: map[string]any{
			"model":   req.Model,
			"prompt":  req.Prompt,
			"size":    req.Size,
			"n":       req.N,
			"quality": req.Quality,
		},
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Accepted(c, task)
}

func (h *ImageTaskHandler) CreateEdit(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, imageTaskMaxRequestSize)
	if err := c.Request.ParseMultipartForm(imageTaskMaxRequestSize); err != nil {
		response.BadRequest(c, "Invalid multipart request: "+err.Error())
		return
	}
	form := c.Request.MultipartForm
	apiKey := strings.TrimSpace(formValue(form, "api_key"))
	if apiKey == "" {
		response.BadRequest(c, "Invalid api_key")
		return
	}
	images, err := readImageTaskUploads(form)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	mask, err := readImageTaskMask(form)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	task, err := h.imageTaskService.Create(c.Request.Context(), service.CreateImageTaskInput{
		UserID:  subject.UserID,
		APIKey:  apiKey,
		Mode:    service.ImageTaskModeEdit,
		Model:   strings.TrimSpace(formValue(form, "model")),
		Prompt:  strings.TrimSpace(formValue(form, "prompt")),
		Size:    strings.TrimSpace(formValue(form, "size")),
		N:       parseImageTaskN(formValue(form, "n")),
		Quality: strings.TrimSpace(formValue(form, "quality")),
		Images:  images,
		Mask:    mask,
		RawFields: map[string]any{
			"model":       formValue(form, "model"),
			"prompt":      formValue(form, "prompt"),
			"size":        formValue(form, "size"),
			"n":           parseImageTaskN(formValue(form, "n")),
			"quality":     formValue(form, "quality"),
			"image_count": len(images),
			"has_mask":    mask != nil,
		},
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Accepted(c, task)
}

func (h *ImageTaskHandler) List(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	page, pageSize := response.ParsePagination(c)
	tasks, result, err := h.imageTaskService.List(c.Request.Context(), subject.UserID, pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortOrder: c.DefaultQuery("sort_order", "desc"),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, tasks, result.Total, page, pageSize)
}

func (h *ImageTaskHandler) Get(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid task ID")
		return
	}
	task, err := h.imageTaskService.Get(c.Request.Context(), subject.UserID, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, task)
}

func formValue(form *multipart.Form, key string) string {
	if form == nil || len(form.Value[key]) == 0 {
		return ""
	}
	return form.Value[key][0]
}

func parseImageTaskN(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func readImageTaskUploads(form *multipart.Form) ([]service.ImageTaskUpload, error) {
	if form == nil {
		return nil, nil
	}
	keys := make([]string, 0, len(form.File))
	for key := range form.File {
		if key == "image" || strings.HasPrefix(key, "image[") {
			keys = append(keys, key)
		}
	}
	sort.SliceStable(keys, func(i, j int) bool {
		if keys[i] == keys[j] {
			return false
		}
		if keys[i] == "image" {
			return true
		}
		if keys[j] == "image" {
			return false
		}
		return keys[i] < keys[j]
	})
	uploads := make([]service.ImageTaskUpload, 0, len(keys))
	for _, key := range keys {
		for _, header := range form.File[key] {
			if len(uploads) >= imageTaskMaxImageCount {
				return nil, errString("too many images")
			}
			upload, err := readImageTaskUpload(key, header)
			if err != nil {
				return nil, err
			}
			uploads = append(uploads, upload)
		}
	}
	return uploads, nil
}

func readImageTaskMask(form *multipart.Form) (*service.ImageTaskUpload, error) {
	if form == nil || len(form.File["mask"]) == 0 {
		return nil, nil
	}
	if len(form.File["mask"]) > 1 {
		return nil, errString("only one mask is allowed")
	}
	upload, err := readImageTaskUpload("mask", form.File["mask"][0])
	if err != nil {
		return nil, err
	}
	return &upload, nil
}

func readImageTaskUpload(fieldName string, header *multipart.FileHeader) (service.ImageTaskUpload, error) {
	if header.Size > imageTaskMaxUploadSize {
		return service.ImageTaskUpload{}, errString("image or mask exceeds 10MB")
	}
	file, err := header.Open()
	if err != nil {
		return service.ImageTaskUpload{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, imageTaskMaxUploadSize+1))
	if err != nil {
		return service.ImageTaskUpload{}, err
	}
	if len(data) > imageTaskMaxUploadSize {
		return service.ImageTaskUpload{}, errString("image or mask exceeds 10MB")
	}
	return service.ImageTaskUpload{
		FieldName:   fieldName,
		FileName:    header.Filename,
		ContentType: header.Header.Get("Content-Type"),
		DataBase64:  base64.StdEncoding.EncodeToString(data),
	}, nil
}

type errString string

func (e errString) Error() string { return string(e) }
