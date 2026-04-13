FROM alpine:3.23
RUN apk add --no-cache ca-certificates
COPY trvl /usr/local/bin/trvl
ENTRYPOINT ["trvl"]
