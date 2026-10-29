package store

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// O envio em pedaços: rascunho → arquivos → partes → Concluir (denuncias.go).
// Cada passo é uma requisição curta. O id do rascunho é aleatório e só quem o
// abriu o conhece; ele não diz nada sobre quem é a pessoa.

// Arquivo é um anexo, de rascunho ou de denúncia.
type Arquivo struct {
	ID       string
	Posicao  int
	Tamanho  int64
	Extensao string
	Tipo     string
	// Limpo: os metadados foram tirados. Sem a marca, o arquivo foi como veio.
	Limpo bool
}

// Partes é quantas partes de TamParte um arquivo de `tamanho` bytes tem.
func Partes(tamanho int64) int { return int((tamanho + TamParte - 1) / TamParte) }

// tamanhoDaParte é quantos bytes a parte n precisa ter (a última é o resto).
func tamanhoDaParte(tamanho int64, n int) int64 {
	return min(TamParte, tamanho-int64(n)*TamParte)
}

var extensaoValida = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

// LimparExtensao aceita só letras e números (até 10); o resto vira "".
func LimparExtensao(ext string) string {
	ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	if !extensaoValida.MatchString(ext) {
		return ""
	}
	return ext
}

// NovoEnvio abre um rascunho. Guarda só a data, para apagar o abandonado.
func (s *Store) NovoEnvio(agora time.Time) (string, error) {
	dia := Dia(agora)
	var hoje int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM envios WHERE criado_dia = ?`, dia).Scan(&hoje); err != nil {
		return "", err
	}
	if hoje >= LimiteEnviosPorDia {
		return "", ErrLimite
	}
	id := novoID()
	_, err := s.db.Exec(`INSERT INTO envios (id, criado_dia) VALUES (?, ?)`, id, dia)
	return id, err
}

// NovoArquivo declara um anexo do rascunho, com o tamanho total que vai chegar.
func (s *Store) NovoArquivo(envioID string, tamanho int64, extensao string) (string, error) {
	if tamanho <= 0 {
		return "", fmt.Errorf("%w: arquivo vazio", ErrInvalid)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var existe, qtd int
	var soma int64
	err = tx.QueryRow(`SELECT (SELECT COUNT(*) FROM envios WHERE id = ?), COUNT(*), COALESCE(SUM(tamanho), 0)
		FROM arquivos WHERE envio_id = ?`, envioID, envioID).Scan(&existe, &qtd, &soma)
	if err != nil {
		return "", err
	}
	if existe == 0 {
		return "", ErrNotFound
	}
	if qtd >= MaxArquivos {
		return "", fmt.Errorf("%w: envie no máximo %d arquivos", ErrInvalid, MaxArquivos)
	}
	if soma+tamanho > MaxTotal {
		return "", fmt.Errorf("%w: os arquivos passam de %s no total", ErrInvalid, RotuloTamanho(MaxTotal))
	}
	// O espaço é reservado pelo tamanho declarado, antes de a primeira parte chegar.
	var guardado int64
	if err := tx.QueryRow(`SELECT COALESCE(SUM(tamanho), 0) FROM arquivos`).Scan(&guardado); err != nil {
		return "", err
	}
	if guardado+tamanho > s.EspacoMax {
		return "", ErrCheio
	}
	id := novoID()
	_, err = tx.Exec(`INSERT INTO arquivos (id, envio_id, posicao, tamanho, extensao) VALUES (?, ?, ?, ?, ?)`,
		id, envioID, qtd, tamanho, LimparExtensao(extensao))
	if err != nil {
		return "", err
	}
	return id, tx.Commit()
}

// GravarParte grava a parte n de um arquivo do rascunho. Repetir a mesma parte
// substitui (o navegador reenvia a que falhou). Na parte 0, o tipo é lido dos bytes.
func (s *Store) GravarParte(envioID, arquivoID string, n int, dados []byte) error {
	var tamanho int64
	err := s.db.QueryRow(`SELECT tamanho FROM arquivos WHERE id = ? AND envio_id = ?`, arquivoID, envioID).Scan(&tamanho)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if n < 0 || n >= Partes(tamanho) {
		return fmt.Errorf("%w: parte fora do arquivo", ErrInvalid)
	}
	if int64(len(dados)) != tamanhoDaParte(tamanho, n) {
		return fmt.Errorf("%w: parte com tamanho errado", ErrInvalid)
	}
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO partes (arquivo_id, n, dados) VALUES (?, ?, ?)`, arquivoID, n, dados); err != nil {
		return err
	}
	if n == 0 {
		_, err = s.db.Exec(`UPDATE arquivos SET tipo = ? WHERE id = ?`, http.DetectContentType(dados), arquivoID)
	}
	return err
}

// ArquivosDoEnvio lista os anexos do rascunho, na ordem em que foram declarados.
func (s *Store) ArquivosDoEnvio(envioID string) ([]Arquivo, error) {
	return s.arquivos(`envio_id`, envioID)
}

func (s *Store) arquivos(coluna, id string) ([]Arquivo, error) {
	rows, err := s.db.Query(`SELECT id, posicao, tamanho, extensao, tipo, limpo FROM arquivos WHERE `+coluna+` = ? ORDER BY posicao`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Arquivo
	for rows.Next() {
		var a Arquivo
		if err := rows.Scan(&a.ID, &a.Posicao, &a.Tamanho, &a.Extensao, &a.Tipo, &a.Limpo); err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// Arquivo devolve os dados de um anexo (sem o conteúdo).
func (s *Store) Arquivo(id string) (Arquivo, error) {
	a := Arquivo{ID: id}
	err := s.db.QueryRow(`SELECT posicao, tamanho, extensao, tipo, limpo FROM arquivos WHERE id = ?`, id).
		Scan(&a.Posicao, &a.Tamanho, &a.Extensao, &a.Tipo, &a.Limpo)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// LerArquivo escreve o conteúdo em w, parte por parte (nunca o arquivo inteiro na memória).
func (s *Store) LerArquivo(id string, w io.Writer) error {
	rows, err := s.db.Query(`SELECT dados FROM partes WHERE arquivo_id = ? ORDER BY n`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var dados []byte
		if err := rows.Scan(&dados); err != nil {
			return err
		}
		if _, err := w.Write(dados); err != nil {
			return err
		}
	}
	return rows.Err()
}

// SubstituirArquivo troca o conteúdo de um anexo (o arquivo limpo no lugar do original).
// Com secure_delete, as partes antigas são zeradas no disco.
func (s *Store) SubstituirArquivo(a Arquivo, dados []byte) error {
	if len(dados) == 0 {
		return fmt.Errorf("%w: arquivo vazio", ErrInvalid)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM partes WHERE arquivo_id = ?`, a.ID); err != nil {
		return err
	}
	for n := range Partes(int64(len(dados))) {
		fim := min(len(dados), (n+1)*TamParte)
		if _, err := tx.Exec(`INSERT INTO partes (arquivo_id, n, dados) VALUES (?, ?, ?)`, a.ID, n, dados[n*TamParte:fim]); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`UPDATE arquivos SET tamanho = ?, tipo = ?, extensao = ?, limpo = ? WHERE id = ?`,
		len(dados), a.Tipo, a.Extensao, a.Limpo, a.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Remendo são bytes a gravar por cima de um arquivo, a partir de Pos (internal/limpeza).
type Remendo struct {
	Pos   int64
	Dados []byte
}

// Remendar grava os remendos por cima do arquivo, sem mudar o tamanho, e o marca como
// limpo. Cada parte tocada é lida, corrigida e regravada uma vez só.
func (s *Store) Remendar(id string, remendos []Remendo) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var tamanho int64
	err = tx.QueryRow(`SELECT tamanho FROM arquivos WHERE id = ?`, id).Scan(&tamanho)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	porParte := map[int64][]Remendo{}
	for _, r := range remendos {
		fim := r.Pos + int64(len(r.Dados))
		if r.Pos < 0 || fim > tamanho {
			return fmt.Errorf("%w: remendo fora do arquivo", ErrInvalid)
		}
		for k := r.Pos / TamParte; k*TamParte < fim; k++ {
			porParte[k] = append(porParte[k], r)
		}
	}
	for k, rs := range porParte {
		if err := remendarParte(tx, id, k, rs); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE arquivos SET limpo = 1 WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func remendarParte(tx *sql.Tx, id string, k int64, remendos []Remendo) error {
	var dados []byte
	if err := tx.QueryRow(`SELECT dados FROM partes WHERE arquivo_id = ? AND n = ?`, id, k).Scan(&dados); err != nil {
		return err
	}
	inicio := k * TamParte
	for _, r := range remendos {
		de := max(r.Pos, inicio)
		ate := min(r.Pos+int64(len(r.Dados)), inicio+int64(len(dados)))
		copy(dados[de-inicio:ate-inicio], r.Dados[de-r.Pos:ate-r.Pos])
	}
	_, err := tx.Exec(`UPDATE partes SET dados = ? WHERE arquivo_id = ? AND n = ?`, dados, id, k)
	return err
}

// Leitor lê qualquer trecho do arquivo direto das partes, sem carregá-lo inteiro: a
// limpeza de um vídeo de 5 GB só lê os cabeçalhos.
func (s *Store) Leitor(a Arquivo) io.ReaderAt {
	return leitor{db: s.db, id: a.ID, tamanho: a.Tamanho}
}

type leitor struct {
	db      *sql.DB
	id      string
	tamanho int64
}

func (l leitor) ReadAt(p []byte, off int64) (int, error) {
	n := 0
	for n < len(p) && off+int64(n) < l.tamanho {
		pos := off + int64(n)
		k, dentro := pos/TamParte, pos%TamParte
		quer := min(int64(len(p)-n), TamParte-dentro, l.tamanho-pos)
		var trecho []byte
		err := l.db.QueryRow(`SELECT substr(dados, ?, ?) FROM partes WHERE arquivo_id = ? AND n = ?`,
			dentro+1, quer, l.id, k).Scan(&trecho)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && len(trecho) == 0) {
			return n, io.ErrUnexpectedEOF
		}
		if err != nil {
			return n, err
		}
		n += copy(p[n:], trecho)
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// LimparRascunhos apaga os rascunhos abertos antes de ontem (abandonados) e o que eles
// tinham. Um rascunho de ontem ainda pode ser concluído hoje, logo depois da meia-noite.
func (s *Store) LimparRascunhos(agora time.Time) (int, error) {
	res, err := s.db.Exec(`DELETE FROM envios WHERE criado_dia < ?`, Dia(agora.Add(-24*time.Hour)))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
