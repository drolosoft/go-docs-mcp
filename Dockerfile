FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /go-docs-mcp .

FROM alpine:3.21
RUN apk add --no-cache poppler-utils tesseract-ocr
COPY --from=build /go-docs-mcp /usr/local/bin/
ENTRYPOINT ["go-docs-mcp"]
