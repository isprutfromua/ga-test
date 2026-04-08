FROM golang:1.22 AS build
WORKDIR /src
COPY . .
RUN go build -o /out/server ./cmd/server

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /out/server /app/server
COPY static /app/static
EXPOSE 8080
ENTRYPOINT ["/app/server"]
