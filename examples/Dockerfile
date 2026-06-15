# Built from the repository root (see docker-compose.yml context) so the
# examples module's `replace github.com/zkrebbekx/filtrx => ../` resolves.
FROM golang:1.24 AS build
WORKDIR /src
COPY . .
WORKDIR /src/examples
RUN CGO_ENABLED=0 go build -o /app .

FROM gcr.io/distroless/base-debian12
COPY --from=build /app /app
EXPOSE 8080
ENTRYPOINT ["/app"]
