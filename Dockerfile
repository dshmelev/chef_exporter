FROM        quay.io/prometheus/busybox:latest
MAINTAINER  Dima Shmelev <avikez@gmail.com>

COPY chef_exporter /bin/chef_exporter

ENTRYPOINT ["/bin/chef_exporter"]
EXPOSE     9101
