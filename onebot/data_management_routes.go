package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const dataExportOptionsMaxBytes = 64 << 10

func dataManagementStatusHandler(service *DataManagementService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			automationWriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		automationWriteJSON(w, http.StatusOK, service.Status())
	}
}

func dataManagementExportHandler(service *DataManagementService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			automationWriteMethodNotAllowed(w, http.MethodPost)
			return
		}
		options, err := decodeDataExportOptions(r)
		if err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", "invalid export options")
			return
		}
		var bundle bytes.Buffer
		if _, err := service.Export(r.Context(), options, &bundle); err != nil {
			automationWriteError(w, http.StatusInternalServerError, "export_failed", "data export failed")
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="wechat-chatter-data.zip"`)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bundle.Bytes())
	}
}

func dataManagementValidateHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			automationWriteMethodNotAllowed(w, http.MethodPost)
			return
		}
		if r.ContentLength > DataBundleMaxBytes {
			automationWriteError(w, http.StatusRequestEntityTooLarge, "bundle_too_large", ErrDataBundleTooLarge.Error())
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, DataBundleMaxBytes+1))
		if err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", "data bundle cannot be read")
			return
		}
		if len(data) > DataBundleMaxBytes {
			automationWriteError(w, http.StatusRequestEntityTooLarge, "bundle_too_large", ErrDataBundleTooLarge.Error())
			return
		}
		validation, err := ValidateDataBundle(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			if errors.Is(err, ErrDataBundleTooLarge) {
				automationWriteError(w, http.StatusRequestEntityTooLarge, "bundle_too_large", err.Error())
				return
			}
			automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		automationWriteJSON(w, http.StatusOK, validation)
	}
}

func NewDataManagementStatusHandler(service *DataManagementService) http.Handler {
	return dataManagementStatusHandler(service)
}

func NewDataManagementExportHandler(service *DataManagementService) http.Handler {
	return dataManagementExportHandler(service)
}

func NewDataManagementValidateHandler() http.Handler {
	return dataManagementValidateHandler()
}

func decodeDataExportOptions(r *http.Request) (DataExportOptions, error) {
	if r.Body == nil {
		return DataExportOptions{}, nil
	}
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, dataExportOptionsMaxBytes))
	decoder.DisallowUnknownFields()
	var options DataExportOptions
	if err := decoder.Decode(&options); err != nil {
		if errors.Is(err, io.EOF) {
			return DataExportOptions{}, nil
		}
		return DataExportOptions{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return DataExportOptions{}, errors.New("request body must contain one JSON object")
	}
	return options, nil
}
