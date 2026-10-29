package db

import (
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
)

func open(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")
	conn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, path
}

func TestOpenAplicaMigrationsUmaVez(t *testing.T) {
	conn, path := open(t)
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("nenhuma migration registrada")
	}
	_, err := conn.Exec(`INSERT INTO denuncias (id, categoria, descricao, recebida_em) VALUES ('d1', 'outro', 'x', '2026-10-28')`)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()

	// Reabrir não pode rodar de novo os CREATE TABLE (dariam erro) nem perder dados.
	conn, err = Open(path)
	if err != nil {
		t.Fatalf("reabrindo: %v", err)
	}
	defer conn.Close()
	var again, denuncias int
	conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&again)
	conn.QueryRow(`SELECT COUNT(*) FROM denuncias`).Scan(&denuncias)
	if again != n || denuncias != 1 {
		t.Errorf("migrations %d→%d, denúncias %d", n, again, denuncias)
	}
}

// A lista de colunas é o contrato do anonimato: coluna nova aqui é decisão
// consciente (e revisão do README), nunca um acréscimo distraído.
func TestColunasNaoIdentificamQuemEnviou(t *testing.T) {
	conn, _ := open(t)
	for tabela, esperadas := range map[string][]string{
		"denuncias": {"id", "categoria", "descricao", "recebida_em", "status", "parecer", "atualizada_em"},
		"envios":    {"id", "criado_dia"},
		"arquivos":  {"id", "envio_id", "denuncia_id", "posicao", "tamanho", "extensao", "tipo", "limpo"},
		"partes":    {"arquivo_id", "n", "dados"},
	} {
		if got := colunas(t, conn, tabela); !slices.Equal(got, esperadas) {
			t.Errorf("%s: colunas %v, esperava %v", tabela, got, esperadas)
		}
	}
}

// Sem rowid, o SQLite não guarda a ordem em que as linhas chegaram.
func TestTabelasSemRowid(t *testing.T) {
	conn, _ := open(t)
	for _, tabela := range []string{"denuncias", "envios", "arquivos", "partes"} {
		if _, err := conn.Exec(`SELECT rowid FROM ` + tabela); err == nil {
			t.Errorf("%s tem rowid", tabela)
		}
	}
}

func TestPragmas(t *testing.T) {
	conn, _ := open(t)
	for _, p := range []string{"foreign_keys", "secure_delete"} {
		var v int
		if err := conn.QueryRow(`PRAGMA ` + p).Scan(&v); err != nil || v != 1 {
			t.Errorf("%s = %d (%v), esperava 1", p, v, err)
		}
	}
}

func TestApagarDenunciaLevaArquivosEPartes(t *testing.T) {
	conn, _ := open(t)
	mustExec(t, conn, `INSERT INTO denuncias (id, categoria, descricao, recebida_em) VALUES ('d1', 'outro', 'x', '2026-10-28')`)
	mustExec(t, conn, `INSERT INTO arquivos (id, denuncia_id, posicao, tamanho) VALUES ('a1', 'd1', 0, 1)`)
	mustExec(t, conn, `INSERT INTO partes (arquivo_id, n, dados) VALUES ('a1', 0, x'ff')`)
	mustExec(t, conn, `DELETE FROM denuncias WHERE id = 'd1'`)
	var n int
	conn.QueryRow(`SELECT (SELECT COUNT(*) FROM arquivos) + (SELECT COUNT(*) FROM partes)`).Scan(&n)
	if n != 0 {
		t.Errorf("sobraram %d linhas", n)
	}
}

// A 003 converte o anexo antigo (um JPEG por linha) em arquivo de uma parte só. É o
// caso do banco que já está no ar: ele recebeu a 001 e a 002 antes da 003 existir.
func TestMigration003ConverteAnexosAntigos(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	conn, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, conn, `CREATE TABLE schema_migrations (name TEXT PRIMARY KEY)`)
	for _, nome := range []string{"migrations/001_denuncias.sql", "migrations/002_users.sql"} {
		if err := apply(conn, nome); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(t, conn, `INSERT INTO denuncias (id, categoria, descricao, recebida_em) VALUES ('d1', 'outro', 'x', '2026-10-28')`)
	mustExec(t, conn, `INSERT INTO anexos (id, denuncia_id, posicao, jpeg) VALUES ('a1', 'd1', 0, x'ffd8ff01')`)
	conn.Close()

	conn, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var tamanho int
	var tipo, ext string
	var limpo bool
	var dados []byte
	err = conn.QueryRow(`SELECT a.tamanho, a.tipo, a.extensao, a.limpo, p.dados FROM arquivos a JOIN partes p ON p.arquivo_id = a.id
		WHERE a.id = 'a1' AND a.denuncia_id = 'd1' AND p.n = 0`).Scan(&tamanho, &tipo, &ext, &limpo, &dados)
	if err != nil || tamanho != 4 || tipo != "image/jpeg" || ext != "jpg" || !limpo || len(dados) != 4 {
		t.Errorf("convertido: %d %q %q %x %v", tamanho, tipo, ext, dados, err)
	}
	if _, err := conn.Exec(`SELECT 1 FROM anexos`); err == nil {
		t.Error("a tabela anexos devia ter saído")
	}
}

func TestOpenFalhaEmCaminhoInvalido(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "nao", "existe", "t.db")); err == nil {
		t.Error("esperava erro")
	}
}

func colunas(t *testing.T, conn *sql.DB, tabela string) []string {
	t.Helper()
	rows, err := conn.Query(`SELECT name FROM pragma_table_info(?) ORDER BY cid`, tabela)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		out = append(out, n)
	}
	return out
}

func mustExec(t *testing.T, conn *sql.DB, q string) {
	t.Helper()
	if _, err := conn.Exec(q); err != nil {
		t.Fatal(err)
	}
}
