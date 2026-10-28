// Package store é o acesso ao SQLite: denúncias, anexos e usuários do painel.
//
// Nada aqui recebe dado de quem denunciou: Registrar só conhece categoria,
// descrição e as imagens já limpas. A data é gravada sem hora.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("não encontrado")
	ErrInvalid  = errors.New("dados inválidos")
	// ErrLimite: o canal recebeu LimitePorDia denúncias hoje. Sem IP nem sessão,
	// o freio contra enxurrada é por dia, para o canal inteiro.
	ErrLimite = errors.New("o canal recebeu muitas denúncias hoje; tente de novo amanhã")
)

const (
	DescricaoMin = 20
	DescricaoMax = 10000
	ParecerMax   = 10000
	MaxImagens   = 3
	LimitePorDia = 30
)

// brasilia é UTC−3 fixo (sem horário de verão desde 2019). Fixo no código para não
// depender do tzdata da imagem.
var brasilia = time.FixedZone("BRT", -3*60*60)

// Dia devolve a data (AAAA-MM-DD) em Brasília. É tudo o que se grava do momento do envio.
func Dia(t time.Time) string { return t.In(brasilia).Format(time.DateOnly) }

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

// novoID gera 128 bits aleatórios: não conta nada, nem a ordem de chegada.
func novoID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
