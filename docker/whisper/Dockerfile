FROM nvidia/cuda:12.4.1-devel-ubuntu22.04

ARG WHISPER_CPP_REF=master

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    cmake \
    build-essential \
    ca-certificates \
    curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /opt

RUN git clone --depth 1 --branch ${WHISPER_CPP_REF} https://github.com/ggml-org/whisper.cpp.git

WORKDIR /opt/whisper.cpp

RUN cmake -B build \
    -DGGML_CUDA=ON \
    -DCMAKE_BUILD_TYPE=Release \
    && cmake --build build --config Release -j"$(nproc)"

RUN mkdir -p /models

EXPOSE 8080

ENTRYPOINT ["/opt/whisper.cpp/build/bin/whisper-server"]

CMD ["--host", "0.0.0.0", "--port", "8080", "--model", "/models/ggml-medium.bin", "--language", "ru"]