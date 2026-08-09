FROM scratch
COPY httprecall /usr/bin/httprecall
ENTRYPOINT ["/usr/bin/httprecall"]
