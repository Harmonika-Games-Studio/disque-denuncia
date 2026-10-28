-- Denúncias. O que NÃO existe aqui é o que garante o anonimato:
-- - nenhuma coluna de quem enviou (IP, navegador, usuário, e-mail, cookie);
-- - nenhuma hora: recebida_em é só a data (AAAA-MM-DD, horário de Brasília).
--   Numa empresa pequena, a hora exata cruza com quem estava online e aponta a pessoa;
-- - WITHOUT ROWID e id aleatório: não sobra nem a ordem de chegada das denúncias.
-- atualizada_em é quando um ADMIN mexeu (status/parecer); nasce nula.
CREATE TABLE denuncias (
	id            TEXT PRIMARY KEY,
	categoria     TEXT NOT NULL,
	descricao     TEXT NOT NULL,
	recebida_em   TEXT NOT NULL,
	status        TEXT NOT NULL DEFAULT 'nova' CHECK (status IN ('nova', 'em_analise', 'concluida')),
	parecer       TEXT NOT NULL DEFAULT '',
	atualizada_em TEXT
) WITHOUT ROWID;

CREATE INDEX denuncias_recebida_em ON denuncias (recebida_em);

-- Imagens como BLOB dentro do banco, e não como arquivo no disco: o sistema de
-- arquivos grava a hora de criação (ctime), que não dá para apagar. Só JPEG
-- re-codificado pelo servidor, sem metadado nenhum.
CREATE TABLE anexos (
	id          TEXT PRIMARY KEY,
	denuncia_id TEXT NOT NULL REFERENCES denuncias (id) ON DELETE CASCADE,
	posicao     INTEGER NOT NULL,
	jpeg        BLOB NOT NULL
) WITHOUT ROWID;

CREATE INDEX anexos_denuncia ON anexos (denuncia_id, posicao);
