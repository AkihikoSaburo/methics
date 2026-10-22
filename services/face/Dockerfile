# ============================================================================
# Multi-stage Dockerfile for Face Recognition API (Production Ready)
# ============================================================================
# Stage 1: Builder — compile & install dependencies into virtualenv
# Stage 2: Runtime — minimal slim image with security hardening
# ============================================================================

# ---------- Stage 1: Builder ----------
FROM python:3.10-slim AS builder

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        build-essential \
        libglib2.0-0 \
        libsm6 \
        libxext6 \
        libxrender-dev && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /build

COPY requirements.txt .
RUN python -m venv /opt/venv && \
    /opt/venv/bin/pip install --no-cache-dir --upgrade pip setuptools wheel && \
    /opt/venv/bin/pip install --no-cache-dir -r requirements.txt


# ---------- Stage 2: Runtime ----------
FROM python:3.10-slim AS runtime

# System runtime libraries for OpenCV
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        libglib2.0-0 \
        libsm6 \
        libxext6 \
        libxrender1 \
        libgl1 && \
    rm -rf /var/lib/apt/lists/*

# Security: create non-root user
RUN useradd -m -u 1000 appuser

# Copy virtualenv from builder
COPY --from=builder /opt/venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH" \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

WORKDIR /app

# Copy application modules and entrypoints
COPY src/ ./src/
COPY main.py api.py ./
COPY models/ ./models/

# Set ownership to non-root user
RUN chown -R appuser:appuser /app
USER appuser

# Environment defaults
ENV DET_MODEL_PATH="models/face_detection/yolo-facev2s.onnx" \
    REC_MODEL_PATH="models/face_recognition/arcface_r50_fp16.onnx" \
    CONF_THRESHOLD="0.35" \
    IOU_THRESHOLD="0.45" \
    SIM_THRESHOLD="0.45" \
    API_HOST="0.0.0.0" \
    API_PORT="8000"

EXPOSE 8000

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD python -c "import urllib.request; urllib.request.urlopen('http://localhost:8000/health')"

# Run uvicorn single worker (in-process ONNX model)
CMD ["uvicorn", "api:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "1"]
