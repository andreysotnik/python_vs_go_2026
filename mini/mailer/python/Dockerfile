# ---- build stage: create an isolated virtualenv with pinned, hash-checked deps ----
FROM python:3.12-slim-bookworm AS builder

ENV PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_NO_CACHE_DIR=1 \
    PYTHONDONTWRITEBYTECODE=1

RUN python -m venv /opt/venv
ENV PATH=/opt/venv/bin:$PATH

WORKDIR /src
COPY requirements.lock ./
RUN pip install --require-hashes -r requirements.lock

COPY pyproject.toml README.md ./
COPY src ./src
RUN pip install --no-deps . && python -m compileall -q /opt/venv

# ---- runtime stage: minimal image, non-root user ----
FROM python:3.12-slim-bookworm AS runtime

ENV PATH=/opt/venv/bin:$PATH \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    STATE_DIR=/var/lib/mailer

RUN groupadd --system --gid 10001 mailer \
    && useradd --system --uid 10001 --gid mailer --home-dir /nonexistent --shell /usr/sbin/nologin mailer \
    && mkdir -p /var/lib/mailer /app/config \
    && chown mailer:mailer /var/lib/mailer

COPY --from=builder /opt/venv /opt/venv

WORKDIR /app
USER 10001:10001
VOLUME ["/var/lib/mailer"]

ENTRYPOINT ["mailer"]
CMD ["run", "--config", "/app/config/jobs.yaml"]
