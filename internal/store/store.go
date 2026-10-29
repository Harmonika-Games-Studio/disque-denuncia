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
	"fmt"
	"time"
)

var (
	ErrNotFound = errors.New("não encontrado")
	ErrInvalid  = errors.New("dados inválidos")
	// ErrLimite: o canal recebeu LimitePorDia denúncias hoje. Sem IP nem sessão,
	// o freio contra enxurrada é por dia, para o canal inteiro.
	ErrLimite = errors.New("o canal recebeu muitas denúncias hoje; tente de novo amanhã")
	// ErrCheio: os arquivos guardados aqui (denúncias ainda não entregues e rascunhos)
	// chegaram a EspacoMax. Esvazia sozinho: a entrega diária apaga o que entregou, e o
	// rascunho abandonado some no dia seguinte.
	ErrCheio = errors.New("o canal está sem espaço para arquivos agora; tente de novo amanhã ou envie arquivos menores")
)

const (
	DescricaoMin = 20
	DescricaoMax = 10000
	ParecerMax   = 10000
	LimitePorDia = 30
	// MaxTotal é a soma dos anexos de uma denúncia; MaxArquivos, quantos. 5 GB é o
	// maior arquivo que o upload simples do Supabase aceita, e cada anexo vai ao OS
	// num PUT só (internal/entrega).
	MaxTotal    = 5 << 30
	MaxArquivos = 50
	// TamParte é o pedaço que cada requisição leva: curto o bastante para não
	// passar dos 60 s do proxy num 4G ruim.
	TamParte = 8 << 20
	// LimiteEnviosPorDia freia rascunhos. Quem segura o disco é o EspacoMax.
	LimiteEnviosPorDia = 100
	// EspacoPadrao é o teto de tudo o que fica guardado aqui ao mesmo tempo. O disco
	// da VPS é dividido com outros serviços, e sem ele 100 rascunhos de 5 GB por dia
	// o encheriam.
	EspacoPadrao int64 = 50 << 30
)

// brasilia é UTC−3 fixo (sem horário de verão desde 2019). Fixo no código para não
// depender do tzdata da imagem.
var brasilia = time.FixedZone("BRT", -3*60*60)

// Dia devolve a data (AAAA-MM-DD) em Brasília. É tudo o que se grava do momento do envio.
func Dia(t time.Time) string { return t.In(brasilia).Format(time.DateOnly) }

// Hora devolve a hora (0 a 23) em Brasília. Serve à entrega diária, nunca à denúncia.
func Hora(t time.Time) int { return t.In(brasilia).Hour() }

type Store struct {
	db *sql.DB
	// EspacoMax é o teto da soma dos arquivos guardados (ESPACO_MAX_GB no main).
	EspacoMax int64
}

func New(db *sql.DB) *Store { return &Store{db: db, EspacoMax: EspacoPadrao} }

// RotuloTamanho escreve um tamanho como as telas mostram: "5 GB", "512 MB".
func RotuloTamanho(b int64) string {
	if b >= 1<<30 && b%(1<<30) == 0 {
		return fmt.Sprintf("%d GB", b>>30)
	}
	return fmt.Sprintf("%d MB", b>>20)
}

// novoID gera 128 bits aleatórios: não conta nada, nem a ordem de chegada.
func novoID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
