FROM golang:1.12 as builder

WORKDIR /app
COPY go.mod .
COPY go.sum .

RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o mjeeves

FROM gcr.io/distroless/base
COPY --from=builder /app/mjeeves /app/
EXPOSE 3000
ENTRYPOINT ["/app/mjeeves"]