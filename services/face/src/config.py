"""
Configuration settings for Face Recognition service and CLI.
Loads settings from environment variables with safe defaults.
"""

import os
from dataclasses import dataclass
from typing import List, Optional


@dataclass(frozen=True)
class Settings:
    # Hugging Face Model Hub Settings
    hf_repo_id: str = os.getenv("HF_REPO_ID", "AkihikoSaburo/methics")
    models_dir: str = os.getenv("MODELS_DIR", "models")
    models_config_file: str = os.getenv("MODELS_CONFIG_FILE", "models_config.json")

    # Model Identifiers or Paths
    det_model_path: str = os.getenv("DET_MODEL_PATH", "face_detection/yolo-facev2s.onnx")
    rec_model_path: str = os.getenv("REC_MODEL_PATH", "face_recognition/arcface_r50_fp16.onnx")

    # Detection & Matching Thresholds
    conf_threshold: float = float(os.getenv("CONF_THRESHOLD", "0.35"))
    iou_threshold: float = float(os.getenv("IOU_THRESHOLD", "0.45"))
    sim_threshold: float = float(os.getenv("SIM_THRESHOLD", "0.45"))

    # Server Settings
    api_host: str = os.getenv("API_HOST", "0.0.0.0")
    api_port: int = int(os.getenv("API_PORT", "8000"))
    log_level: str = os.getenv("LOG_LEVEL", "INFO").upper()

    # Image Processing Defaults
    aligned_face_size: int = int(os.getenv("ALIGNED_FACE_SIZE", "112"))
    input_tensor_size: int = int(os.getenv("INPUT_TENSOR_SIZE", "640"))


settings = Settings()
