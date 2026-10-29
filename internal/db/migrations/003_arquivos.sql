-- Anexos de qualquer tipo, até 5 GB por denúncia (store.MaxTotal), enviados em pedaços.
--
-- O navegador abre um rascunho (envios), declara cada arquivo (arquivos) e manda
-- partes de até 8 MB (partes), cada uma numa requisição curta: o proxy da VPS corta
-- requisição com mais de 60 s, e uma parte que falha é reenviada sozinha. Ao
-- concluir, os arquivos passam do rascunho para a denúncia.
--
-- As partes ficam no próprio SQLite, nunca em arquivo: arquivo tem hora de criação.
-- O rascunho guarda só a DATA (criado_dia), para ser apagado se abandonado. Nenhuma
-- tabela tem rowid: não sobra a ordem de chegada.
CREATE TABLE envios (
	id         TEXT PRIMARY KEY,
	criado_dia TEXT NOT NULL
) WITHOUT ROWID;

CREATE INDEX envios_criado_dia ON envios (criado_dia);

-- extensao vem só da extensão do nome (o navegador nunca manda o nome inteiro);
-- tipo é o que os próprios bytes dizem (http.DetectContentType da primeira parte);
-- limpo = 1 quando os metadados foram tirados (internal/limpeza, internal/imagem).
CREATE TABLE arquivos (
	id          TEXT PRIMARY KEY,
	envio_id    TEXT REFERENCES envios (id) ON DELETE CASCADE,
	denuncia_id TEXT REFERENCES denuncias (id) ON DELETE CASCADE,
	posicao     INTEGER NOT NULL,
	tamanho     INTEGER NOT NULL CHECK (tamanho > 0),
	extensao    TEXT NOT NULL DEFAULT '',
	tipo        TEXT NOT NULL DEFAULT '',
	limpo       INTEGER NOT NULL DEFAULT 0 CHECK (limpo IN (0, 1))
) WITHOUT ROWID;

CREATE INDEX arquivos_envio ON arquivos (envio_id, posicao);
CREATE INDEX arquivos_denuncia ON arquivos (denuncia_id, posicao);

CREATE TABLE partes (
	arquivo_id TEXT NOT NULL REFERENCES arquivos (id) ON DELETE CASCADE,
	n          INTEGER NOT NULL,
	dados      BLOB NOT NULL,
	PRIMARY KEY (arquivo_id, n)
) WITHOUT ROWID;

-- O que já existia (JPEG limpo, um BLOB por anexo) vira arquivo de uma parte só.
INSERT INTO arquivos (id, denuncia_id, posicao, tamanho, extensao, tipo, limpo)
	SELECT id, denuncia_id, posicao, length(jpeg), 'jpg', 'image/jpeg', 1 FROM anexos;
INSERT INTO partes (arquivo_id, n, dados) SELECT id, 0, jpeg FROM anexos;
DROP TABLE anexos;
