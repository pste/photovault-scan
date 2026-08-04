## STAGE 1 ##################################
FROM golang:1.25-alpine AS build

# CGO disattivato: binario statico, nessuna libreria di sistema da trascinare
# nell'immagine finale.
ENV CGO_ENABLED=0

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY internal/ internal/
RUN go build -trimpath -ldflags="-s -w" -o /out/scan ./cmd/scan

## STAGE 2 ##################################
FROM alpine:3.21

# ffmpeg: fotogrammi dei video e ffprobe per i metadati.
# libheif-tools: heif-convert per le foto HEIC dell'iPhone.
RUN apk add --no-cache ffmpeg libheif-tools tzdata

COPY --from=build /out/scan /usr/local/bin/scan

USER 1000:1000
ENTRYPOINT ["/usr/local/bin/scan"]
