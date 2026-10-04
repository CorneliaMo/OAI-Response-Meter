package dashboard

import (
	"database/sql"
	"errors"
	"github.com/cornelia/oai-response-meter/internal/pricing"
	"net/http"
)

type PromptMetadata struct {
	Model        string `json:"model"`
	Hash         string `json:"hash"`
	SourceLabel  string `json:"source_label"`
	FirstSeen    string `json:"first_seen"`
	LastSeen     string `json:"last_seen"`
	Observations int64  `json:"observations"`
	Characters   int64  `json:"characters"`
}
type PromptDetail struct {
	PromptMetadata
	Text string `json:"text"`
}

func (s apiServer) handlePrompts(w http.ResponseWriter, r *http.Request) {
	resp := struct {
		Models   []string         `json:"models"`
		Versions []PromptMetadata `json:"versions"`
	}{Models: []string{}, Versions: []PromptMetadata{}}
	rows, err := s.db.QueryContext(r.Context(), `select distinct model from prompt_versions order by model`)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	for rows.Next() {
		var model string
		if err = rows.Scan(&model); err != nil {
			break
		}
		resp.Models = append(resp.Models, model)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		writeError(w, 500, err)
		return
	}
	query := `select model,hash,source_label,first_seen,last_seen,observations,characters from prompt_versions`
	var args []any
	model := pricing.CanonicalModelName(r.URL.Query().Get("model"))
	if model != "" {
		query += ` where model=?`
		args = append(args, model)
	}
	query += ` order by julianday(last_seen) desc,model,hash`
	if model == "" {
		query += ` limit 100`
	}
	rows, err = s.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var p PromptMetadata
		if err = rows.Scan(&p.Model, &p.Hash, &p.SourceLabel, &p.FirstSeen, &p.LastSeen, &p.Observations, &p.Characters); err != nil {
			writeError(w, 500, err)
			return
		}
		resp.Versions = append(resp.Versions, p)
	}
	if err = rows.Err(); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, resp)
}
func (s apiServer) handlePrompt(w http.ResponseWriter, r *http.Request) {
	var p PromptDetail
	err := s.db.QueryRowContext(r.Context(), `select model,hash,source_label,first_seen,last_seen,observations,characters,text from prompt_versions where model=? and hash=?`, pricing.CanonicalModelName(r.URL.Query().Get("model")), r.URL.Query().Get("hash")).Scan(&p.Model, &p.Hash, &p.SourceLabel, &p.FirstSeen, &p.LastSeen, &p.Observations, &p.Characters, &p.Text)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, errors.New("prompt not found"))
		return
	}
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, p)
}
