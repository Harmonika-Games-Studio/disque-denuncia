package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Nova é o texto que a página pública manda ao concluir. Os anexos já estão no rascunho.
type Nova struct {
	Categoria string
	Descricao string
}

type Denuncia struct {
	ID           string
	Categoria    string
	Descricao    string
	RecebidaEm   string
	Status       string
	Parecer      string
	AtualizadaEm string
	Anexos       []Arquivo // na ordem do envio (em Listar, só a quantidade conta)
}

// Validar confere o texto antes de qualquer trabalho com os anexos.
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
	return nil
}

// Concluir transforma o rascunho em denúncia, numa transação só: confere que cada
// anexo chegou inteiro, grava a denúncia (só com a data) e passa os anexos para ela.
func (s *Store) Concluir(envioID string, n Nova, agora time.Time) error {
	if err := n.Validar(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var existe int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM envios WHERE id = ?`, envioID).Scan(&existe); err != nil {
		return err
	}
	if existe == 0 {
		return ErrNotFound
	}
	if err := conferirCompletos(tx, envioID); err != nil {
		return err
	}
	dia := Dia(agora)
	var hoje int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM denuncias WHERE recebida_em = ?`, dia).Scan(&hoje); err != nil {
		return err
	}
	if hoje >= LimitePorDia {
		return ErrLimite
	}
	return gravarDenuncia(tx, envioID, n, dia)
}

func gravarDenuncia(tx *sql.Tx, envioID string, n Nova, dia string) error {
	id := novoID()
	_, err := tx.Exec(`INSERT INTO denuncias (id, categoria, descricao, recebida_em) VALUES (?, ?, ?, ?)`,
		id, n.Categoria, strings.TrimSpace(n.Descricao), dia)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE arquivos SET denuncia_id = ?, envio_id = NULL WHERE envio_id = ?`, id, envioID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM envios WHERE id = ?`, envioID); err != nil {
		return err
	}
	return tx.Commit()
}

// conferirCompletos recusa o rascunho se algum anexo não chegou inteiro.
func conferirCompletos(tx *sql.Tx, envioID string) error {
	rows, err := tx.Query(`
		SELECT a.posicao, a.tamanho, COUNT(p.n), COALESCE(SUM(length(p.dados)), 0)
		  FROM arquivos a LEFT JOIN partes p ON p.arquivo_id = a.id
		 WHERE a.envio_id = ?
		 GROUP BY a.id, a.posicao, a.tamanho`, envioID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var pos, partes int
		var tamanho, soma int64
		if err := rows.Scan(&pos, &tamanho, &partes, &soma); err != nil {
			return err
		}
		if partes != Partes(tamanho) || soma != tamanho {
			return fmt.Errorf("%w: o arquivo %d não chegou inteiro; envie de novo", ErrInvalid, pos+1)
		}
	}
	return rows.Err()
}

// ArquivoPronto é um anexo com o conteúdo inteiro em mãos (formulário sem JavaScript, testes).
type ArquivoPronto struct {
	Dados    []byte
	Extensao string
}

// Rascunho abre um rascunho já com os anexos inteiros (o formulário sem JavaScript).
// Daí em diante é o mesmo caminho do envio em pedaços: limpar e Concluir.
func (s *Store) Rascunho(anexos []ArquivoPronto, agora time.Time) (string, error) {
	envio, err := s.NovoEnvio(agora)
	if err != nil {
		return "", err
	}
	for _, a := range anexos {
		if err := s.gravarInteiro(envio, a); err != nil {
			s.Descartar(envio)
			return "", err
		}
	}
	return envio, nil
}

// Descartar apaga um rascunho e os arquivos dele (envio que deu errado no meio).
func (s *Store) Descartar(envio string) error {
	_, err := s.db.Exec(`DELETE FROM envios WHERE id = ?`, envio)
	return err
}

// Registrar faz rascunho e Concluir de uma vez, sem limpar nada: os anexos têm que
// chegar limpos. Serve aos testes de quem precisa de denúncias prontas no banco.
func (s *Store) Registrar(n Nova, anexos []ArquivoPronto, agora time.Time) error {
	if err := n.Validar(); err != nil {
		return err
	}
	envio, err := s.Rascunho(anexos, agora)
	if err != nil {
		return err
	}
	if err := s.Concluir(envio, n, agora); err != nil {
		s.Descartar(envio)
		return err
	}
	return nil
}

func (s *Store) gravarInteiro(envio string, a ArquivoPronto) error {
	id, err := s.NovoArquivo(envio, int64(len(a.Dados)), a.Extensao)
	if err != nil {
		return err
	}
	for k := range Partes(int64(len(a.Dados))) {
		if err := s.GravarParte(envio, id, k, a.Dados[k*TamParte:min(len(a.Dados), (k+1)*TamParte)]); err != nil {
			return err
		}
	}
	return nil
}

// Listar traz as denúncias do status pedido (vazio = todas), das mais recentes para as
// mais antigas. Dentro do mesmo dia a ordem é a do id aleatório: não há outra.
func (s *Store) Listar(status string) ([]Denuncia, error) {
	rows, err := s.db.Query(`
		SELECT d.id, d.categoria, d.descricao, d.recebida_em, d.status, d.parecer, COALESCE(d.atualizada_em, ''),
		       (SELECT COUNT(*) FROM arquivos a WHERE a.denuncia_id = d.id)
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
		d.Anexos = make([]Arquivo, anexos)
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
	d.Anexos, err = s.arquivos(`denuncia_id`, id)
	return d, err
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

// Pendentes traz as denúncias recebidas ANTES do dia `hoje` (AAAA-MM-DD), com os
// anexos. Só dias completos saem: entregar a de hoje às 08:00 contaria que ela
// chegou de madrugada.
func (s *Store) Pendentes(hoje string) ([]Denuncia, error) {
	rows, err := s.db.Query(`SELECT id FROM denuncias WHERE recebida_em < ? ORDER BY recebida_em, id`, hoje)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	list := make([]Denuncia, 0, len(ids))
	for _, id := range ids {
		d, err := s.Buscar(id)
		if err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, nil
}

// Remover apaga a denúncia, os anexos e as partes (ON DELETE CASCADE). Com
// secure_delete, o espaço que ocupavam no arquivo é zerado.
func (s *Store) Remover(id string) error {
	_, err := s.db.Exec(`DELETE FROM denuncias WHERE id = ?`, id)
	return err
}
