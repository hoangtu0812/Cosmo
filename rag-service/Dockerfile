# CPU-only torch. The GPU wheels are several gigabytes larger and this service
# does retrieval, not training; inference itself lives behind the LLM gateway.
FROM python:3.12-slim

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    HF_HOME=/models \
    MODEL_CACHE=/models

WORKDIR /srv

COPY requirements.txt ./
RUN pip install --no-cache-dir --extra-index-url https://download.pytorch.org/whl/cpu -r requirements.txt

COPY requirements-dev.txt ./
RUN pip install --no-cache-dir -r requirements-dev.txt

COPY app ./app
COPY tests ./tests

EXPOSE 8000

# Probed with the interpreter that is already here rather than pulling curl in
# from the distribution repositories: one less package, one less thing that has
# to be reachable at build time.
HEALTHCHECK --interval=15s --timeout=5s --retries=10 \
    CMD ["python", "-c", "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://localhost:8000/health', timeout=4).status == 200 else 1)"]

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]
