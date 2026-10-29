-- Quem denuncia escolhe, a cada envio, quando a denúncia vai para o HarmonikaOS:
--
--   0 = no dia seguinte, a partir de ENTREGA_HORA (o padrão): o OS só fica sabendo a data;
--   1 = agora, em poucos minutos: o horário de chegada no OS mostra a hora aproximada do
--       envio. A página explica a diferença, e a escolha é da pessoa.
--
-- Aqui continua sem hora: a coluna só diz qual dos dois caminhos ela escolheu.
ALTER TABLE denuncias ADD COLUMN imediata INTEGER NOT NULL DEFAULT 0 CHECK (imediata IN (0, 1));
