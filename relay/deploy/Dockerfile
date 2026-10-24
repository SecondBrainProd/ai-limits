# Build context: repository root.
#   docker build -f relay/deploy/Dockerfile .
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY relay ./relay
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ailimits-relay ./relay/cmd/ailimits-relay

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ailimits-relay /ailimits-relay
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/ailimits-relay", "-addr", ":8080", "-db", "/data/ailimits.db"]
