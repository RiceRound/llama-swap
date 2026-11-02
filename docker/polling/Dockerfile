# Build the Linux amd64 binary on the development machine first.
# The Docker CLI lets configured cmd/cmdStop manage sibling containers.
FROM docker:cli
COPY --chmod=755 build/llama-swap-linux-amd64 /usr/local/bin/llama-swap
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/llama-swap"]
CMD ["-config", "/config/config.yaml", "-listen", ":8080"]
