# syntax=docker/dockerfile:1
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOFLAGS=-mod=vendor go build -trimpath -ldflags='-s -w' -o /out/slackinviter .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/slackinviter /app/slackinviter
COPY templates/ /app/templates/
COPY static/ /app/static/
ENV SLACKINVITER_PORT=8080
EXPOSE 8080
ENTRYPOINT ["/app/slackinviter"]
