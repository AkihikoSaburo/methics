"""
Integrated Face Detection and Recognition Pipeline.
"""

import logging
import threading
from typing import List, Dict, Any, Optional
import numpy as np

from .detector import YOLOFaceDetector
from .aligner import FaceAligner
from .recognizer import FaceRecognizer, ArcFaceRecognizer
from .config import settings
from .model_manager import get_model_manager

logger = logging.getLogger(__name__)


class FacePipeline:
    """
    High-level orchestrator connecting YOLO Face Detection,
    Face Alignment, and Feature Recognition.
    Auto-downloads models from Hugging Face if not available locally.
    """

    def __init__(
        self,
        det_model_path: Optional[str] = None,
        rec_model_path: Optional[str] = None,
        conf_threshold: Optional[float] = None,
        iou_threshold: Optional[float] = None
    ):
        model_mgr = get_model_manager()

        # Resolve detection model
        det_id = det_model_path or settings.det_model_path
        self.det_model_path = model_mgr.ensure_model(det_id, category="face_detection")
        det_cfg = model_mgr.get_model_config(self.det_model_path, category="face_detection")

        self.conf_threshold = conf_threshold if conf_threshold is not None else det_cfg.get("conf_threshold", settings.conf_threshold)
        self.iou_threshold = iou_threshold if iou_threshold is not None else det_cfg.get("iou_threshold", settings.iou_threshold)

        logger.info(f"Initializing YOLO Face Detector from: {self.det_model_path}")
        self.detector = YOLOFaceDetector(
            self.det_model_path,
            conf_threshold=self.conf_threshold,
            iou_threshold=self.iou_threshold
        )

        # Resolve recognition model
        rec_id = rec_model_path or settings.rec_model_path
        self.rec_model_path = model_mgr.ensure_model(rec_id, category="face_recognition")
        rec_cfg = model_mgr.get_model_config(self.rec_model_path, category="face_recognition")

        raw_size = rec_cfg.get("input_size", (settings.aligned_face_size, settings.aligned_face_size))
        if isinstance(raw_size, list):
            self.rec_input_size = (int(raw_size[0]), int(raw_size[1]))
        elif isinstance(raw_size, (int, float)):
            self.rec_input_size = (int(raw_size), int(raw_size))
        else:
            self.rec_input_size = tuple(raw_size)

        self.norm_type = rec_cfg.get("norm_type", "insightface")

        logger.info(f"Initializing Face Aligner (target size: {self.rec_input_size})")
        self.aligner = FaceAligner(output_size=self.rec_input_size)

        logger.info(f"Initializing Face Recognizer ({self.rec_input_size}, norm={self.norm_type}) from: {self.rec_model_path}")
        self.recognizer = FaceRecognizer(
            self.rec_model_path,
            input_size=self.rec_input_size,
            norm_type=self.norm_type
        )

    def process_image(
        self,
        img_bgr: np.ndarray,
        conf_threshold: Optional[float] = None,
        iou_threshold: Optional[float] = None
    ) -> List[Dict[str, Any]]:
        """
        Detect, align, and extract face embeddings for all faces in image.
        """
        boxes, scores = self.detector.detect(
            img_bgr,
            conf_threshold=conf_threshold,
            iou_threshold=iou_threshold
        )
        results = []

        for box, score in zip(boxes, scores):
            aligned_face = self.aligner.align(img_bgr, bbox=box)
            embedding = self.recognizer.extract_embedding(aligned_face)

            results.append({
                "bbox": box.tolist(),
                "confidence": float(score),
                "aligned_face": aligned_face,
                "embedding": embedding
            })

        return results




# ============================================================================
# Thread-Safe Singleton Pipeline Loader (for API / application reuse)
# ============================================================================
_pipeline_instance: Optional[FacePipeline] = None
_pipeline_lock = threading.Lock()


def get_pipeline(
    det_model_path: Optional[str] = None,
    rec_model_path: Optional[str] = None,
    conf_threshold: Optional[float] = None,
    iou_threshold: Optional[float] = None,
    force_reload: bool = False
) -> FacePipeline:
    """
    Return a thread-safe singleton FacePipeline instance.
    Models are loaded once and reused across requests unless force_reload is True.
    """
    global _pipeline_instance
    with _pipeline_lock:
        if _pipeline_instance is None or force_reload:
            _pipeline_instance = FacePipeline(
                det_model_path=det_model_path,
                rec_model_path=rec_model_path,
                conf_threshold=conf_threshold,
                iou_threshold=iou_threshold,
            )
    return _pipeline_instance
