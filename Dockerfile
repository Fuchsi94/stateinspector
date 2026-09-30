ARG GO_VERSION=1.27
FROM golang:${GO_VERSION} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/stateinspector ./cmd/stateinspector

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/stateinspector /stateinspector
USER 65532:65532
ENTRYPOINT ["/stateinspector"]
