FROM golang:1.25-alpine AS builder
ARG MAIN_PATH=init-node
ARG TARGETARCH
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o app ./${MAIN_PATH}

FROM alpine:3.22
COPY --from=builder /app/app /usr/local/bin/app
CMD ["app"]
