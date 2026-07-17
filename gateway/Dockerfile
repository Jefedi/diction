FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder

# TARGETOS/TARGETARCH are provided automatically by buildx for cross-compiling.
# Empty when using the classic builder → go falls back to the native target.
ARG TARGETOS
ARG TARGETARCH

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY core/ core/

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags="-s -w" -o gateway .

FROM alpine:latest

RUN apk add --no-cache ca-certificates ffmpeg
COPY --from=builder /build/gateway /usr/local/bin/gateway

EXPOSE 8080

ENTRYPOINT ["gateway"]
