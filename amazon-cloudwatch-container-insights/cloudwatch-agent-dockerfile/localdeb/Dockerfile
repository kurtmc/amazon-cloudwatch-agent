ARG BUILD_IMAGE=ubuntu:latest
FROM $BUILD_IMAGE as build

ARG TARGETARCH

RUN apt-get update &&  \
    apt-get install -y ca-certificates && \
    rm -rf /var/lib/apt/lists/*

COPY ${TARGETARCH}/amazon-cloudwatch-agent.deb /tmp/amazon-cloudwatch-agent.deb

RUN dpkg -i -E /tmp/amazon-cloudwatch-agent.deb && \
    rm -rf /tmp/* && \
    rm -rf /var/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-config-wizard && \
    rm -rf /var/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl && \
    rm -rf /var/aws/amazon-cloudwatch-agent/bin/config-downloader

FROM scratch

COPY --from=build /tmp /tmp
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /var/aws/amazon-cloudwatch-agent /var/aws/amazon-cloudwatch-agent

ENV RUN_IN_CONTAINER="True"
ENTRYPOINT ["/var/aws/amazon-cloudwatch-agent/bin/start-amazon-cloudwatch-agent"]
