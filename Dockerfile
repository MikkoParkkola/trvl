FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
RUN apk upgrade --no-cache && apk add --no-cache ca-certificates \
    && addgroup -S trvl \
    && adduser -S -G trvl -h /home/trvl trvl
COPY trvl /usr/local/bin/trvl
USER trvl
WORKDIR /home/trvl
ENTRYPOINT ["trvl"]
