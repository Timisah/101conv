FROM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
ARG APP_VERSION=0.1.0
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.buildVersion=${APP_VERSION}" -o /out/101conv ./cmd/101conv

FROM scratch
COPY --from=build /out/101conv /101conv
USER 65532:65532
ENV APP_VERSION=0.1.0 PORT=5656
EXPOSE 5656
ENTRYPOINT ["/101conv"]
