# disque-denuncia

Canal anônimo de denúncias da Harmonika. Um binário Go, no molde do
[health-monitor](https://github.com/Harmonika-Games-Studio/health-monitor): templ + htmx,
SQLite em Go puro, imagem distroless.

- `/`: página pública, sem login. Quem denuncia escolhe o assunto, descreve e, se quiser,
  anexa até 3 imagens.
- **Entrega ao HarmonikaOS**: uma vez por dia, as denúncias dos dias anteriores vão para
  o HarmonikaOS (Admin › Denúncias), onde a administração trata. Entregue e confirmada,
  a denúncia é apagada daqui.
- `/admin`: painel com login (HTTP Basic, tabela `users`). Mostra o que ainda não foi
  entregue ao OS (ou tudo, se a entrega estiver desligada).

O código é pequeno de propósito: dá para conferir tudo o que está abaixo lendo uns
poucos arquivos.

## O que o sistema garante

| Garantia | Onde conferir |
| --- | --- |
| Não guarda IP, navegador, cookie, login nem e-mail de quem denuncia. | `internal/db/migrations/001_denuncias.sql`: não existe coluna para isso. `internal/web/publico.go`: só lê `categoria`, `descricao` e `imagens`. |
| Não guarda a **hora** do envio, só a data (horário de Brasília). Numa empresa pequena, a hora cruza com quem estava online. | `recebida_em` é `AAAA-MM-DD` (`store.Dia`). |
| Não guarda a ordem de chegada. | Tabelas `WITHOUT ROWID`, id de 128 bits aleatório (`store.novoID`). |
| Apaga o metadado das fotos (modelo do celular, GPS, data da foto, autor) e o nome do arquivo. | `internal/imagem/limpar.go`: decodifica até virar pixels e grava um JPEG novo. O nome do arquivo e o tipo informado pelo navegador são ignorados. |
| Imagens não viram arquivo no disco (arquivo tem hora de criação). | Ficam como BLOB no SQLite. O formulário cabe inteiro na memória (`maxMemoria = maxCorpo`), então nem o arquivo temporário do `net/http` é criado. |
| O navegador de quem denuncia não fala com nenhum outro servidor. | `Content-Security-Policy: default-src 'none'` em toda resposta; o htmx vem embutido no binário (`internal/web/static`), sem CDN e sem fonte externa. |
| Sem log de acesso. | Não há middleware de log. O `ErrorLog` do `http.Server` vai para `io.Discard` (o do Go escreve o IP do cliente quando um handler entra em pânico). O único log é `erro interno` com a mensagem do erro, sem caminho, IP nem conteúdo. |
| Sem cache e sem `Referer`. | `Cache-Control: no-store` e `Referrer-Policy: no-referrer` em toda resposta. |
| Apagado é apagado de verdade. | `PRAGMA secure_delete` (o SQLite zera o que é removido). |
| A entrega ao HarmonikaOS não conta a hora do envio, nem o IP. | `internal/entrega`: sai só o lote dos **dias anteriores**, a partir de `ENTREGA_HORA`; o request parte deste servidor; o JSON leva só id, categoria, descrição, data e quantas imagens. |
| Depois de entregue, não fica cópia aqui. | `entrega.Tentar` só chama `Remover` depois de o OS confirmar o texto e cada imagem. |

### A trava automática

`internal/web/anonimato_test.go` lê todo o código-fonte (Go e templ) e **reprova** o
que identificaria quem denuncia: `RemoteAddr`, `X-Forwarded-For`, `X-Real-Ip`,
`User-Agent`, `Referer`, qualquer cookie, nome do arquivo enviado (`.Filename`), CDN ou
analytics. A única leitura de cabeçalho permitida é `HX-Request`.
`internal/db/db_test.go` trava a lista de colunas das tabelas. Para mudar uma dessas
regras, mude este README junto: é o que a empresa promete a quem denuncia.

### O que o sistema NÃO consegue garantir

- **A rede.** O provedor de hospedagem (Railway, o proxy na frente) e a rede de onde a
  pessoa acessa (o Wi-Fi da empresa, a operadora) veem o IP de quem acessa, nos logs
  deles. O código não controla isso. Por isso a página orienta a enviar do celular
  pessoal, fora da rede da empresa.
- **O texto.** Quem escreve "eu, que sento ao lado do Fulano" se identificou. A página
  avisa.
- **O limite diário.** Sem IP nem sessão, o freio contra enxurrada é por canal:
  30 denúncias por dia (`store.LimitePorDia`). Alguém mal-intencionado consegue esgotar
  o dia. É a troca consciente: limitar por IP exigiria guardar o IP.

Não há protocolo nem acompanhamento: depois de enviada, ninguém consegue ligar a
denúncia a quem enviou, nem a própria pessoa.

## Entrega ao HarmonikaOS

A cada 10 minutos, se já passou de `ENTREGA_HORA` (horário de Brasília), o canal entrega
as denúncias com data **anterior a hoje**:

```
POST {OS_URL}/api/denuncias/receber                    {"id","categoria","descricao","recebida_em","total_imagens"}
PUT  {OS_URL}/api/denuncias/receber/{id}/imagens/{n}   o JPEG n, uma imagem por request (limite de 4,5 MB da Vercel)
```

Os dois levam `Authorization: Bearer {OS_TOKEN}`. O token é gerado pelo admin no
HarmonikaOS, em **Admin › Denúncias › Token do canal**, e aparece uma vez só (o OS guarda
apenas o hash). A empresa das denúncias é a dona do token.

**Rotacionar:** gere um token novo na mesma tela (o anterior para de valer na hora), troque
o `OS_TOKEN` deste serviço e reinicie. No intervalo, as entregas recebem 401 e as denúncias
esperam aqui; nada se perde. **Desativar** na mesma tela apaga o token e o OS para de
aceitar entregas.

Os dois lados são idempotentes pelo id: se a entrega cair no meio, a denúncia continua aqui e a
próxima tentativa repete tudo sem duplicar. Só depois de tudo confirmado ela é apagada
daqui.

Por que só dias completos: entregar às 08:00 a denúncia de hoje contaria que ela chegou
de madrugada. Saindo só as de ontem para trás, o horário da entrega não diz nada além da
data, e uma nova tentativa depois de falha pode acontecer a qualquer hora.

## Conferir em 5 minutos

```bash
go test ./...                                   # inclui a trava de anonimato
cat internal/db/migrations/001_denuncias.sql    # o que é guardado
cat internal/web/publico.go                     # tudo o que a página pública lê
sha384sum internal/web/static/htmx.min.js | xxd -r -p | base64
# deve dar HGfztofotfshcF7+8n44JQL2oJmowVChPTg48S+jvZoztPfvwD79OC/LTtG6dMp+
# (o SRI oficial do htmx 2.0.4)
```

## Rodar

```bash
go run .                       # DB_PATH=denuncias.db ADDR=127.0.0.1:8080 EMPRESA=Harmonika
templ generate -path internal/web   # só ao mexer em views.templ (o _templ.go vai no repositório)
```

Criar quem entra no painel (sem usuário, ninguém entra):

```bash
htpasswd -nBC 10 usuario | cut -d: -f2          # gera o hash bcrypt
sqlite3 denuncias.db "INSERT INTO users VALUES ('usuario', '<hash>')"
```

No contêiner (o volume `/data` guarda o banco):

```bash
docker build -t disque-denuncia .
docker run -d --name disque-denuncia -p 127.0.0.1:8080:8080 -v disque-denuncia-data:/data disque-denuncia
```

Variáveis: `DB_PATH` (padrão `denuncias.db`), `ADDR` (padrão `127.0.0.1:8080`),
`EMPRESA` (nome no título da página, padrão `Harmonika`), `OS_URL` (base do HarmonikaOS;
vazio = entrega desligada), `OS_TOKEN` (obrigatório com `OS_URL`), `ENTREGA_HORA`
(padrão `8`).

Publique **só com HTTPS**: o login do painel é HTTP Basic, e a denúncia não pode
trafegar aberta.

## Estrutura

```
main.go                         sobe o servidor (sem log de acesso)
internal/db/                    abre o SQLite e aplica as migrations embutidas
internal/store/                 denúncias, anexos, usuários
internal/imagem/                re-codifica a imagem e apaga os metadados
internal/entrega/               entrega diária ao HarmonikaOS e remoção local
internal/web/publico.go         página pública e envio
internal/web/admin.go           painel (atrás do login)
internal/web/server.go          rotas, cabeçalhos de segurança, login
internal/web/views.templ        HTML (views_templ.go é gerado)
internal/web/anonimato_test.go  a trava do anonimato
```
