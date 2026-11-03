"""
Face Detection (SCRFD) & Face Recognition (ArcFace) Package.
"""

from .config import settings, Settings
from .scrfd_detector import SCRFDDetector
from .aligner import FaceAligner
from .recognizer import FaceRecognizer, ArcFaceRecognizer
from .pipeline import FacePipeline, get_pipeline
from .model_manager import ModelManager, get_model_manager
from .utils import (
    ARCFACE_DST_112,
    nms,
)

__all__ = [
    "settings",
    "Settings",
    "SCRFDDetector",
    "FaceAligner",
    "FaceRecognizer",
    "ArcFaceRecognizer",
    "FacePipeline",
    "get_pipeline",
    "ModelManager",
    "get_model_manager",
    "ARCFACE_DST_112",
    "nms",
]
