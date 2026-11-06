package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Autumn-27/artex/db"
)

const (
	maxTaskNameRunes           = 200
	maxTaskMetadataRequestSize = 16 << 10
)

// updateTaskMetadata updates editable metadata and the live asset-sharing policy.
func (s *Server) updateTaskMetadata(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if _, ok := s.m.Task(taskID); !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTaskMetadataRequestSize)
	var request struct {
		Name              *string `json:"name"`
		Remark            *string `json:"remark"`
		Pinned            *bool   `json:"pinned"`
		ShareGlobalAssets *bool   `json:"share_global_assets"`
	}
	if err := decode(r, &request); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErr(w, http.StatusRequestEntityTooLarge, "Request body too large")
		} else {
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	if request.Name == nil && request.Pinned == nil && request.Remark == nil && request.ShareGlobalAssets == nil {
		writeErr(w, http.StatusBadRequest, "At least one of name, pinned, remark or share_global_assets must be provided")
		return
	}
	if request.Name != nil {
		name := strings.TrimSpace(*request.Name)
		if name == "" {
			writeErr(w, http.StatusBadRequest, "Task name cannot be empty")
			return
		}
		if utf8.RuneCountInString(name) > maxTaskNameRunes {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("Task name at most %d characters", maxTaskNameRunes))
			return
		}
		request.Name = &name
	}

	if request.Remark != nil {
		remark, err := normalizeRemark(*request.Remark)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		request.Remark = &remark
	}
	task, err := s.m.UpdateTaskMetadata(taskID, db.TaskPatch{Name: request.Name, Pinned: request.Pinned, Remark: request.Remark, ShareGlobalAssets: request.ShareGlobalAssets})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if task == nil {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	writeJSON(w, http.StatusOK, taskDTO(task, s.resolvedTaskStatus(task)))
}
