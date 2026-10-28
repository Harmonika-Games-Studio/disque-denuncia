package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Nova é o que a página pública manda. As imagens já chegam limpas (pacote imagem).
type Nova struct {
	Categoria string
	Descricao string
	Imagens   [][]byte
}

type Denuncia struct {
	ID           string
	Categoria    string
	Descricao    string
	RecebidaEm   string
	Status       string
	Parecer      string
	AtualizadaEm string
	Anexos       []string // ids, na ordem do envio
}

// Validar confere a denúncia antes de qualquer trabalho com as imagens.
func (n Nova) Validar() error {
	if !valido(Categorias, n.Categoria) {
		return fmt.Errorf("%w: escolha o assunto da denúncia", ErrInvalid)
	}
	tam := utf8.RuneCountInString(strings.TrimSpace(n.Descricao))
	if tam < DescricaoMin {
		return fmt.Errorf("%w: descreva o que aconteceu com pelo menos %d caracteres", ErrInvalid, DescricaoMin)
	}
	if tam > DescricaoMax {
		return fmt.Errorf("%w: a descrição passa de %d caracteres", ErrInvalid, DescricaoMax)
	}
	if len(n.Imagens) > MaxImagens {
		return fmt.Errorf("%w: envie no máximo %d imagens", ErrInvalid, MaxImagens)
	}
	return nil
}

// Registrar grava a denúncia e as imagens numa transação só. `agora` decide só a data.
func (s *Store) Registrar(n Nova, agora time.Time) error {
	if err := n.Validar(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	dia := Dia(agora)
	var hoje int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM denuncias WHERE recebida_em = ?`, dia).Scan(&hoje); err != nil {
		return err
	}
	if hoje >= LimitePorDia {
		return ErrLimite
	}

	id := novoID()
	_, err = tx.Exec(`INSERT INTO denuncias (id, categoria, descricao, recebida_em) VALUES (?, ?, ?, ?)`,
		id, n.Categoria, strings.TrimSpace(n.Descricao), dia)
	if err != nil {
		return err
	}
	for i, jpeg := range n.Imagens {
		_, err := tx.Exec(`INSERT INTO anexos (id, denuncia_id, posicao, jpeg) VALUES (?, ?, ?, ?)`, novoID(), id, i, jpeg)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Listar traz as denúncias do status pedido (vazio = todas), das mais recentes para as
// mais antigas. Dentro do mesmo dia a ordem é a do id aleatório: não há outra.
func (s *Store) Listar(status string) ([]Denuncia, error) {
	rows, err := s.db.Query(`
		SELECT d.id, d.categoria, d.descricao, d.recebida_em, d.status, d.parecer, COALESCE(d.atualizada_em, ''),
		       (SELECT COUNT(*) FROM anexos a WHERE a.denuncia_id = d.id)
		  FROM denuncias d
		 WHERE ? = '' OR d.status = ?
		 ORDER BY d.recebida_em DESC, d.id`, status, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Denuncia
	for rows.Next() {
		var d Denuncia
		var anexos int
		err := rows.Scan(&d.ID, &d.Categoria, &d.Descricao, &d.RecebidaEm, &d.Status, &d.Parecer, &d.AtualizadaEm, &anexos)
		if err != nil {
			return nil, err
		}
		d.Anexos = make([]string, anexos)
		list = append(list, d)
	}
	return list, rows.Err()
}

func (s *Store) Buscar(id string) (Denuncia, error) {
	d := Denuncia{ID: id}
	err := s.db.QueryRow(`SELECT categoria, descricao, recebida_em, status, parecer, COALESCE(atualizada_em, '')
		FROM denuncias WHERE id = ?`, id).
		Scan(&d.Categoria, &d.Descricao, &d.RecebidaEm, &d.Status, &d.Parecer, &d.AtualizadaEm)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	d.Anexos, err = s.anexosDe(id)
	return d, err
}

func (s *Store) anexosDe(id string) ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM anexos WHERE denuncia_id = ? ORDER BY posicao`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		ids = append(ids, a)
	}
	return ids, rows.Err()
}

// Anexo devolve o JPEG de um anexo.
func (s *Store) Anexo(id string) ([]byte, error) {
	var jpeg []byte
	err := s.db.QueryRow(`SELECT jpeg FROM anexos WHERE id = ?`, id).Scan(&jpeg)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return jpeg, err
}

// Atualizar muda o status e o parecer. Categoria, descrição e anexos são a denúncia
// e não mudam. atualizada_em é a hora em que o admin mexeu, não a do envio.
func (s *Store) Atualizar(id, status, parecer string, agora time.Time) error {
	if !valido(Status, status) {
		return fmt.Errorf("%w: status inválido", ErrInvalid)
	}
	parecer = strings.TrimSpace(parecer)
	if utf8.RuneCountInString(parecer) > ParecerMax {
		return fmt.Errorf("%w: o parecer passa de %d caracteres", ErrInvalid, ParecerMax)
	}
	res, err := s.db.Exec(`UPDATE denuncias SET status = ?, parecer = ?, atualizada_em = ? WHERE id = ?`,
		status, parecer, agora.In(brasilia).Format("2006-01-02 15:04"), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
