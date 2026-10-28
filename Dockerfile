# disque-denuncia: binário estático (SQLite em Go puro, sem cgo) numa imagem distroless.
# Build:  docker build -t disque-denuncia .
# Rodar:  docker run -d --name disque-denuncia -p 127.0.0.1:8080:8080 -v disque-denuncia-data:/data disque-denuncia

FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# O views_templ.go gerado já vem no repositório, então não precisa do templ aqui.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/disque-denuncia . \
    && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/disque-denuncia /disque-denuncia
# /data nasce do usuário nonroot para o volume herdar o dono e o SQLite conseguir gravar.
COPY --from=build --chown=nonroot:nonroot /out/data /data

# ADDR em 0.0.0.0 porque dentro do contêiner o localhost não é alcançável de fora.
# EMPRESA é o nome que aparece no título da página pública.
ENV DB_PATH=/data/denuncias.db \
    ADDR=0.0.0.0:8080 \
    EMPRESA=Harmonika
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/disque-denuncia"]
