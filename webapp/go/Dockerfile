FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /isubid .

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends default-mysql-client \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /isubid /usr/local/bin/isubid
ENV ISUBID_PORT=8000
EXPOSE 8000
CMD ["isubid"]
