FROM golang:1.27.1-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/movie-helper ./cmd/bot && mkdir /out/data

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build /out/movie-helper /movie-helper
USER 65532:65532
ENV DB_PATH=/data/movie-helper.db
ENTRYPOINT ["/movie-helper"]
