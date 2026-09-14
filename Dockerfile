FROM node:24-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
COPY internal/ ./internal/
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/metrics-generator .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/metrics-generator /metrics-generator
EXPOSE 8080
ENTRYPOINT ["/metrics-generator"]
