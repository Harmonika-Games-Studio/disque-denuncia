# disque-denuncia

Canal anônimo de denúncias da Harmonika. Um binário Go, no molde do
[health-monitor](https://github.com/Harmonika-Games-Studio/health-monitor): templ + htmx,
SQLite em Go puro, imagem distroless.

- `/`: página pública, sem login. Quem denuncia escolhe o assunto, descreve e, se quiser,
  anexa arquivos de qualquer tipo (fotos, prints, PDF, vídeo, áudio): até 50 arquivos e
  5 GB no total, enviados em partes de 8 MB, com barra de progresso.
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
| Não guarda IP, navegador, cookie, login nem e-mail de quem denuncia. | `internal/db/migrations/001_denuncias.sql`: não existe coluna para isso. `internal/web/publico.go`: só lê `categoria`, `descricao` e, de cada arquivo, o tamanho, a extensão e o conteúdo. |
| Não guarda a **hora** do envio, só a data (horário de Brasília). Numa empresa pequena, a hora cruza com quem estava online. | `recebida_em` é `AAAA-MM-DD` (`store.Dia`). |
| Não guarda a ordem de chegada. | Tabelas `WITHOUT ROWID`, id de 128 bits aleatório (`store.novoID`). |
| Apaga o metadado das fotos JPG, PNG e WebP (modelo do celular, GPS, data da foto, autor). | `internal/imagem/limpar.go`: decodifica até virar pixels e grava um JPEG novo, ao concluir o envio (`internal/web/limpeza.go`). |
| Apaga o metadado de vídeo e áudio do celular (MP4, MOV, M4A, 3GP, MP3): GPS, aparelho, data da gravação, artista. | `internal/limpeza/mp4.go` e `mp3.go`: as caixas `udta`, `meta` e `uuid` viram `free` zeradas, as datas de `mvhd`/`tkhd`/`mdhd` viram zero e as etiquetas ID3 ficam vazias. Remendo no lugar, sem mudar o tamanho: vale para um vídeo de 5 GB. |
| Apaga o metadado de Word, Excel, PowerPoint, LibreOffice, PDF e GIF: autor, quem comentou ou alterou, e-mail de login, empresa, caminhos do computador, impressora, datas, versões antigas do PDF, EXIF das fotos coladas. | `internal/limpeza/office.go`, `pdf.go` ([pdfcpu](https://github.com/pdfcpu/pdfcpu), sem rede e sem arquivo de configuração) e `imagens.go`. Regravados até 128 MB. O PDF regravado não leva a hora da limpeza (Info vazio, ID aleatório). |
| Reabre o ZIP comum: cada arquivo de dentro perde o nome (vira `001.jpg`, `002.pdf`…), a data, os campos extras e o comentário; `__MACOSX`, `._*`, `.DS_Store` e `Thumbs.db` somem; e cada arquivo de dentro passa pela limpeza do tipo dele, ZIP dentro de ZIP incluso. | `internal/limpeza/zip.go`. Regravado até 128 MB, até 2000 arquivos e 3 níveis. Se algum arquivo de dentro é de um tipo que a limpeza não conhece, o ZIP sai **sem** a marca de limpo. |
| Cada anexo diz se foi limpo. | Coluna `arquivos.limpo`, que vai ao OS no campo `limpo`. O painel e o OS avisam nos anexos que vieram como foram enviados. |
| Nunca recebe o nome do arquivo. | `internal/web/static/canal.js` manda só a extensão; o tipo é detectado pelo conteúdo (`http.DetectContentType`), não pelo que o navegador diz. |
| Anexos não viram arquivo no disco (arquivo tem hora de criação). | Ficam como BLOB no SQLite, em partes de 8 MB (tabela `partes`). Cada parte e o formulário sem JavaScript (até 20 MB) cabem na memória, então nem o arquivo temporário do `net/http` é criado. |
| A URL que o navegador chama não leva nada que fique no banco. | O envio em pedaços usa só o id do rascunho e a **posição** do arquivo nele (`PUT /envios/{id}/arquivos/{posicao}/partes/{n}`); os dois somem ao concluir. O id do anexo, que sobrevive à conclusão e aparece em `/admin/arquivos/{id}`, nunca sai do servidor: se saísse, o log de acesso do proxy (IP, hora, caminho) ligaria quem enviou à denúncia. `TestIdDoAnexoNuncaSaiParaONavegador` confere. |
| O envio em andamento não deixa rastro de hora. | O rascunho (`envios`) guarda só a data; rascunho abandonado é apagado no dia seguinte (`LimparRascunhos`). |
| O navegador de quem denuncia não fala com nenhum outro servidor. | `Content-Security-Policy: default-src 'none'` em toda resposta; o htmx vem embutido no binário (`internal/web/static`), sem CDN e sem fonte externa. |
| Sem log de acesso. | Não há middleware de log. O `ErrorLog` do `http.Server` vai para `io.Discard` (o do Go escreve o IP do cliente quando um handler entra em pânico). O único log é `erro interno` com a mensagem do erro, sem caminho, IP nem conteúdo. |
| Sem cache e sem `Referer`. | `Cache-Control: no-store` e `Referrer-Policy: no-referrer` em toda resposta. |
| Apagado é apagado de verdade. | `PRAGMA secure_delete` (o SQLite zera o que é removido). |
| A entrega ao HarmonikaOS não conta a hora do envio, nem o IP. | `internal/entrega`: sai só o lote dos **dias anteriores**, a partir de `ENTREGA_HORA`; o request parte deste servidor; o JSON leva só id, categoria, descrição, data e, de cada anexo, tipo, tamanho e extensão. |
| Depois de entregue, não fica cópia aqui. | `entrega.Tentar` só chama `Remover` depois de o OS confirmar o texto e cada anexo. |

### A trava automática

`internal/web/anonimato_test.go` lê todo o código-fonte (Go, templ e o `canal.js`) e
**reprova** o que identificaria quem denuncia: `RemoteAddr`, `X-Forwarded-For`,
`X-Real-Ip`, `User-Agent`, `Referer`, qualquer cookie, nome do arquivo enviado
(`.Filename`), CDN ou analytics; no navegador, `localStorage`, `sessionStorage`,
`indexedDB`, `navigator.` e `eval`. A única leitura de cabeçalho permitida é `HX-Request`.
`internal/db/db_test.go` trava a lista de colunas das tabelas. Para mudar uma dessas
regras, mude este README junto: é o que a empresa promete a quem denuncia.

### O que o sistema NÃO consegue garantir

- **O conteúdo dos arquivos.** A limpeza tira o que o programa grava escondido, não o
  que está à vista: a voz num áudio, o que aparece num vídeo, um nome escrito no
  documento, uma assinatura escaneada.
- **Os tipos que a limpeza não conhece**, e os que ela não consegue abrir: HEIC (a
  foto do iPhone quando o navegador não a converte), WebM, telemetria de GoPro e drone
  (vem como faixa de dados dentro do vídeo), PDF ou ZIP com senha, documento, PDF ou ZIP
  acima de 128 MB. Esses vão como estão, soltos ou dentro de um ZIP. O canal aceita e
  **avisa** (em "Saiba mais"), e o painel e o OS marcam esses anexos.
- **A rede.** O provedor de hospedagem (a VPS, o proxy na frente) e a rede de onde a
  pessoa acessa (o Wi-Fi da empresa, a operadora) veem o IP de quem acessa, nos logs
  deles. O código não controla isso. Por isso a página orienta a enviar do celular
  pessoal, fora da rede da empresa.
- **O texto.** Quem escreve "eu, que sento ao lado do Fulano" se identificou. A página
  avisa.
- **O limite diário.** Sem IP nem sessão, o freio contra enxurrada é por canal:
  30 denúncias e 100 rascunhos por dia (`store.LimitePorDia`, `store.LimiteEnviosPorDia`),
  e no máximo `ESPACO_MAX_GB` (padrão 50 GB) de arquivos guardados ao mesmo tempo. Passou
  disso, o canal responde "sem espaço" até a entrega diária ou a limpeza dos rascunhos
  abandonados liberarem. Alguém mal-intencionado consegue esgotar
  o dia. É a troca consciente: limitar por IP exigiria guardar o IP.

Não há protocolo nem acompanhamento: depois de enviada, ninguém consegue ligar a
denúncia a quem enviou, nem a própria pessoa.

## Entrega ao HarmonikaOS

A cada 10 minutos, se já passou de `ENTREGA_HORA` (horário de Brasília), o canal entrega
as denúncias com data **anterior a hoje**:

```
POST {OS_URL}/api/denuncias/receber                   {"id","categoria","descricao","recebida_em","anexos":[{"tipo","tamanho","extensao"}]}
POST {OS_URL}/api/denuncias/receber/{id}/anexos/{n}   → {"existe":true} ou {"url": URL assinada de upload do Storage}
PUT  {url assinada}                                   o anexo n, lido do SQLite parte por parte
```

O corpo de uma rota da Vercel não passa de 4,5 MB, e um anexo vai a 5 GB: por isso o
arquivo sobe direto no Storage do Supabase, por uma URL assinada que o OS gera. Os dois
`POST` levam `Authorization: Bearer {OS_TOKEN}`; o `PUT` não (quem autoriza é a URL
assinada, e ela nunca vai para o log: o erro mostra só o caminho, sem o token). O token é gerado pelo admin no
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
cat internal/db/migrations/*.sql                # o que é guardado
cat internal/web/publico.go                     # tudo o que a página pública lê
cat internal/web/static/canal.js                # tudo o que o navegador faz
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
`EMPRESA` (nome no título da página, padrão `Harmonika`), `REPO_URL` (link do código na
página, padrão este repositório), `OS_URL` (base do HarmonikaOS;
vazio = entrega desligada), `OS_TOKEN` (obrigatório com `OS_URL`), `ENTREGA_HORA`
(padrão `8`), `ESPACO_MAX_GB` (teto do que fica guardado aqui ao mesmo tempo, padrão `50`;
o disco da VPS é dividido com outros serviços).

Publique **só com HTTPS**: o login do painel é HTTP Basic, e a denúncia não pode
trafegar aberta. O proxy na frente precisa aceitar corpo de 8 MB por requisição (uma
parte); nenhuma requisição carrega o arquivo inteiro.

## Estrutura

```
main.go                         sobe o servidor (sem log de acesso)
internal/db/                    abre o SQLite e aplica as migrations embutidas
internal/store/                 denúncias, rascunhos e anexos em partes, usuários
internal/imagem/                re-codifica a foto e apaga os metadados
internal/limpeza/               apaga os metadados de vídeo, áudio, Office, LibreOffice, PDF e GIF
internal/web/limpeza.go         decide, por anexo, como limpar (ao concluir o envio)
internal/entrega/               entrega diária ao HarmonikaOS e remoção local
internal/web/publico.go         página pública e envio
internal/web/admin.go           painel (atrás do login)
internal/web/server.go          rotas, cabeçalhos de segurança, login
internal/web/views.templ        HTML (views_templ.go é gerado)
internal/web/static/canal.js    envio em partes com progresso (sem ele, o formulário vai inteiro)
internal/web/anonimato_test.go  a trava do anonimato
```
