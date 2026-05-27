package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type imageTaskRepository struct {
	db *sql.DB
}

func NewImageTaskRepository(db *sql.DB) service.ImageTaskRepository {
	return &imageTaskRepository{db: db}
}

func (r *imageTaskRepository) Create(ctx context.Context, task *service.ImageTask) error {
	now := time.Now()
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO image_tasks (
			user_id, mode, status, model, prompt, size, n, quality,
			request_json, input_images_json, input_mask_json, created_at, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12)
		RETURNING id, created_at, updated_at
	`,
		task.UserID, task.Mode, task.Status, task.Model, task.Prompt, task.Size,
		task.N, task.Quality, task.RequestJSON, task.InputImagesJSON, task.InputMaskJSON, now,
	).Scan(&task.ID, &task.CreatedAt, &task.UpdatedAt)
	return err
}

func (r *imageTaskRepository) GetByID(ctx context.Context, id int64) (*service.ImageTask, error) {
	return r.get(ctx, "WHERE id = $1", id)
}

func (r *imageTaskRepository) GetByIDForUser(ctx context.Context, userID, id int64) (*service.ImageTask, error) {
	return r.get(ctx, "WHERE user_id = $1 AND id = $2", userID, id)
}

func (r *imageTaskRepository) ListByUser(ctx context.Context, userID int64, params pagination.PaginationParams) ([]service.ImageTask, *pagination.PaginationResult, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM image_tasks WHERE user_id = $1", userID).Scan(&total); err != nil {
		return nil, nil, err
	}

	order := "DESC"
	if params.NormalizedSortOrder(pagination.SortOrderDesc) == pagination.SortOrderAsc {
		order = "ASC"
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, user_id, mode, status, model, prompt, size, n, quality,
			'' AS request_json, '' AS response_json, error_message, '' AS input_images_json, '' AS input_mask_json,
			started_at, finished_at, created_at, updated_at
		FROM image_tasks
		WHERE user_id = $1
		ORDER BY created_at `+order+`, id `+order+`
		LIMIT $2 OFFSET $3
	`, userID, params.Limit(), params.Offset())
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	tasks := make([]service.ImageTask, 0)
	for rows.Next() {
		task, err := scanImageTask(rows)
		if err != nil {
			return nil, nil, err
		}
		tasks = append(tasks, *task)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return tasks, paginationResultFromTotal(total, params), nil
}

func (r *imageTaskRepository) MarkRunning(ctx context.Context, id int64, startedAt time.Time) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE image_tasks
		SET status = $1, started_at = $2, updated_at = $2
		WHERE id = $3 AND status = $4
	`, service.ImageTaskStatusRunning, startedAt, id, service.ImageTaskStatusPending)
	return imageTaskUpdateResult(res, err)
}

func (r *imageTaskRepository) MarkSucceeded(ctx context.Context, id int64, responseJSON string, finishedAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE image_tasks
		SET status = $1, response_json = $2, finished_at = $3, updated_at = $3
		WHERE id = $4
	`, service.ImageTaskStatusSucceeded, responseJSON, finishedAt, id)
	return err
}

func (r *imageTaskRepository) MarkFailed(ctx context.Context, id int64, errorMessage string, finishedAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE image_tasks
		SET status = $1, error_message = $2, finished_at = $3, updated_at = $3
		WHERE id = $4
	`, service.ImageTaskStatusFailed, errorMessage, finishedAt, id)
	return err
}

func (r *imageTaskRepository) FailStale(ctx context.Context, cutoff time.Time, errorMessage string) (int, error) {
	now := time.Now()
	res, err := r.db.ExecContext(ctx, `
		UPDATE image_tasks
		SET status = $1, error_message = $2, finished_at = $3, updated_at = $3
		WHERE status IN ($4, $5) AND updated_at < $6
	`, service.ImageTaskStatusFailed, errorMessage, now, service.ImageTaskStatusPending, service.ImageTaskStatusRunning, cutoff)
	return imageTaskRowsAffected(res, err)
}

func (r *imageTaskRepository) DeleteFinishedBefore(ctx context.Context, cutoff time.Time) (int, error) {
	res, err := r.db.ExecContext(ctx, `
		DELETE FROM image_tasks
		WHERE status IN ($1, $2) AND finished_at < $3
	`, service.ImageTaskStatusSucceeded, service.ImageTaskStatusFailed, cutoff)
	return imageTaskRowsAffected(res, err)
}

func (r *imageTaskRepository) get(ctx context.Context, where string, args ...any) (*service.ImageTask, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, user_id, mode, status, model, prompt, size, n, quality,
			request_json, response_json, error_message, input_images_json, input_mask_json,
			started_at, finished_at, created_at, updated_at
		FROM image_tasks
		`+where+`
	`, args...)
	task, err := scanImageTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrImageTaskNotFound
	}
	return task, err
}

type imageTaskScanner interface {
	Scan(dest ...any) error
}

func scanImageTask(scanner imageTaskScanner) (*service.ImageTask, error) {
	var task service.ImageTask
	if err := scanner.Scan(
		&task.ID,
		&task.UserID,
		&task.Mode,
		&task.Status,
		&task.Model,
		&task.Prompt,
		&task.Size,
		&task.N,
		&task.Quality,
		&task.RequestJSON,
		&task.ResponseJSON,
		&task.ErrorMessage,
		&task.InputImagesJSON,
		&task.InputMaskJSON,
		&task.StartedAt,
		&task.FinishedAt,
		&task.CreatedAt,
		&task.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &task, nil
}

func imageTaskRowsAffected(res sql.Result, err error) (int, error) {
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func imageTaskUpdateResult(res sql.Result, err error) error {
	n, err := imageTaskRowsAffected(res, err)
	if err != nil {
		return err
	}
	if n == 0 {
		return service.ErrImageTaskNotFound
	}
	return nil
}
