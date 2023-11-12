FROM golang:1.21-alpine3.18 AS builder

WORKDIR /go/src/github.com/polykit/peertube-autoscale-runners
ADD go.mod go.sum ./
RUN go mod download
ADD cache/main.go .
RUN CGO_ENABLED=0 go build -v -o /dev/null
ADD . .
RUN CGO_ENABLED=0 go build -v -o /peertube-autoscale-runners

FROM alpine:3.18
LABEL maintainer="github@polykit.rocks"
LABEL source_repository="https://github.com/polykit/peertube-autoscale-runners"

RUN apk --no-cache add curl jq
COPY --from=builder /peertube-autoscale-runners /peertube-autoscale-runners

RUN curl -Lo /usr/local/bin/kubectl "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" \
    && chmod +x /usr/local/bin/kubectl \
    && kubectl version --client

CMD ["/peertube-autoscale-runners"]
