FROM golang:1.26-alpine AS builder

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/manapool-jobs .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata \
	&& addgroup -S jobs && adduser -S jobs -G jobs

WORKDIR /app
COPY --from=builder /out/manapool-jobs /app/manapool-jobs
USER jobs

ENTRYPOINT ["/app/manapool-jobs"]
