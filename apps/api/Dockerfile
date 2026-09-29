FROM golang:1.25-alpine AS build
WORKDIR /app

COPY apps/api/go.mod apps/api/go.sum ./
RUN go mod download

COPY apps/api/ ./
RUN CGO_ENABLED=0 go build -mod=readonly -o /runtime .

FROM alpine:3.23
WORKDIR /app

RUN addgroup -S -g 10001 appgroup && adduser -S -u 10001 appuser -G appgroup

COPY --from=build --chown=appuser:appgroup /runtime ./runtime

ENV API_ADDR=0.0.0.0:3000
ENV RUST_RPC_ADDR=maruvo-rust:50051

USER appuser:appgroup
EXPOSE 3000

CMD ["./runtime"]
