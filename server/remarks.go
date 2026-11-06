package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Autumn-27/artex/db"
)

const maxRemarkRunes = db.MaxTaskRemarkRunes

func normalizeRemark(value string) (string, error) {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > maxRemarkRunes {
		return "", fmt.Errorf("Remark max %d characters", maxRemarkRunes)
	}
	return value, nil
}

func (s *Server) updateScheduleMetadata(w http.ResponseWriter, r *http.Request) {
	id, err := parseScheduleID(r)
	if err != nil || id <= 0 {
		writeErr(w, 400, "Schedule ID invalid")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTaskMetadataRequestSize)
	var request struct {
		Name   *string `json:"name"`
		Remark *string `json:"remark"`
	}
	if err := decode(r, &request); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErr(w, 413, "Request body too large")
		} else {
			writeErr(w, 400, err.Error())
		}
		return
	}
	if request.Name == nil && request.Remark == nil {
		writeErr(w, 400, "Need to provide name or remark")
		return
	}
	if request.Name != nil {
		name := strings.TrimSpace(*request.Name)
		if name == "" || utf8.RuneCountInString(name) > maxTaskNameRunes {
			writeErr(w, 400, fmt.Sprintf("Plan name must be 1 to %d characters", maxTaskNameRunes))
			return
		}
		request.Name = &name
	}
	if request.Remark != nil {
		remark, err := normalizeRemark(*request.Remark)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		request.Remark = &remark
	}
	item, err := s.m.pg.UpdateTaskScheduleMetadata(id, db.TaskSchedulePatch{Name: request.Name, Remark: request.Remark})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if item == nil {
		writeErr(w, 404, "Schedule not found")
		return
	}
	writeJSON(w, 200, item)
}
