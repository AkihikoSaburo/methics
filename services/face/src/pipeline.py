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

    def compare_faces(
        self,
        img1_bgr: np.ndarray,
        img2_bgr: np.ndarray,
        similarity_threshold: Optional[float] = None,
        conf_threshold: Optional[float] = None,
        iou_threshold: Optional[float] = None
    ) -> Dict[str, Any]:
        """
        Detect faces in two images, align, and compare the most prominent face from each.
        """
        sim_thresh = similarity_threshold if similarity_threshold is not None else settings.sim_threshold

        faces1 = self.process_image(img1_bgr, conf_threshold=conf_threshold, iou_threshold=iou_threshold)
        faces2 = self.process_image(img2_bgr, conf_threshold=conf_threshold, iou_threshold=iou_threshold)

        if not faces1:
            return {"error": "No face detected in Image 1", "match": False}
        if not faces2:
            return {"error": "No face detected in Image 2", "match": False}

        # Select most confident face from each image
        face1 = max(faces1, key=lambda f: f["confidence"])
        face2 = max(faces2, key=lambda f: f["confidence"])

        sim = self.recognizer.compute_similarity(face1["embedding"], face2["embedding"])
        dist = self.recognizer.compute_distance(face1["embedding"], face2["embedding"])
        is_match = sim >= sim_thresh

        return {
            "similarity": sim,
            "euclidean_distance": dist,
            "threshold": sim_thresh,
            "match": is_match,
            "face1_confidence": face1["confidence"],
            "face2_confidence": face2["confidence"],
            "face1_aligned": face1["aligned_face"],
            "face2_aligned": face2["aligned_face"],
        }


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
