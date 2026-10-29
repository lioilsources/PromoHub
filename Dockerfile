# promohub for JODA (linux/amd64). Pure Go, static binary on distroless.
FROM golang:1.26-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/promohub ./cmd/promohub

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/promohub /usr/local/bin/promohub
ENV LISTEN_ADDR=:8080 CONFIG_DIR=/config
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s CMD ["/usr/local/bin/promohub", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/promohub"]
CMD ["serve"]
