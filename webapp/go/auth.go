package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12 // 意図的に遅い実装: コスト過剰

type authRequest struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type userResponse struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (h *handler) postRegister(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, err := h.db.ExecContext(r.Context(),
		"INSERT INTO users (name, password_hash) VALUES (?, ?)", req.Name, string(hash))
	if err != nil {
		writeError(w, http.StatusConflict, "name already taken")
		return
	}
	id, _ := res.LastInsertId()
	if err := setLogin(w, r, id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, userResponse{ID: id, Name: req.Name})
}

func (h *handler) postLogin(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var u struct {
		ID           int64  `db:"id"`
		Name         string `db:"name"`
		PasswordHash string `db:"password_hash"`
	}
	err := h.db.GetContext(r.Context(), &u,
		"SELECT id, name, password_hash FROM users WHERE name = ?", req.Name)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "invalid name or password")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "invalid name or password")
		return
	}
	if err := setLogin(w, r, u.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, userResponse{ID: u.ID, Name: u.Name})
}
