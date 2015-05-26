# docker build -t seafile-uploader .
# docker run -e "SEAFILE_URL=https://seafile-hostname.com" -e "SEAFILE_TOKEN=15f1fdbf20b1bd85a3cf2447ab7347c1a7771825" -e "SEAFILE_PROXY_LISTEN=:8080" --publish 8888:8080 --name test2 --rm seafile-uploader
FROM golang:onbuild
MAINTAINER lazureykis@gmail.com
EXPOSE 8080
