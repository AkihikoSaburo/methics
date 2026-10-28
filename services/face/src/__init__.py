"""
Face Detection (YOLO-FaceV2) & Face Recognition Package.
"""

from .config import settings, Settings
from .detector import YOLOFaceDetector
from .aligner import FaceAligner
from .recognizer import FaceRecognizer, ArcFaceRecognizer
from .pipeline import FacePipeline, get_pipeline
from .model_manager import ModelManager, get_model_manager
from .utils import (
    ARCFACE_DST_112,
    letterbox,
    nms,
    sigmoid,
)

__all__ = [
    "settings",
    "Settings",
    "YOLOFaceDetector",
    "FaceAligner",
    "FaceRecognizer",
    "ArcFaceRecognizer",
    "FacePipeline",
    "get_pipeline",
    "ModelManager",
    "get_model_manager",
    "ARCFACE_DST_112",
    "letterbox",
    "nms",
    "sigmoid",
]
