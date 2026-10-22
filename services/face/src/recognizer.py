"""
Face Recognition feature extractor module using ONNX Runtime.
Supports InsightFace (ArcFace, CosFace, MobileFaceNet) and customizable architectures.
"""

import os
import logging
from typing import List, Optional, Tuple
import cv2
import numpy as np
import onnxruntime as ort

logger = logging.getLogger(__name__)


class FaceRecognizer:
    """
    ONNX Feature Extractor for Face Recognition models.
    Default configuration conforms to ArcFace / InsightFace conventions
    (112x112 aligned face crops, normalized, producing L2-normalized embeddings).
    """

    def __init__(
        self,
        model_path: str,
        input_size: Tuple[int, int] = (112, 112),
        norm_type: str = "insightface",
        providers: Optional[List[str]] = None
    ):
        if not os.path.exists(model_path):
            raise FileNotFoundError(f"Recognizer model file not found: {model_path}")

        self.model_path = model_path
        self.input_size = tuple(input_size)
        self.norm_type = norm_type

        if providers is None:
            available = ort.get_available_providers()
            providers = (
                ['CUDAExecutionProvider', 'CPUExecutionProvider']
                if 'CUDAExecutionProvider' in available
                else ['CPUExecutionProvider']
            )

        logger.info(f"Loading Face Recognizer ({self.input_size}, norm={self.norm_type}) from {model_path}")
        self.session = ort.InferenceSession(model_path, providers=providers)
        self.input_name = self.session.get_inputs()[0].name

    def preprocess(self, aligned_bgr: np.ndarray) -> np.ndarray:
        """
        Preprocess face image:
        - Resize to expected input_size (default: 112x112)
        - Convert BGR to RGB
        - Normalize according to norm_type
        - Transpose: (H, W, C) -> (1, C, H, W)
        """
        if aligned_bgr is None or aligned_bgr.size == 0:
            raise ValueError("Invalid aligned face: image is empty or None")

        h, w = self.input_size
        if aligned_bgr.shape[0] != h or aligned_bgr.shape[1] != w:
            aligned_bgr = cv2.resize(aligned_bgr, (w, h), interpolation=cv2.INTER_LINEAR)

        rgb = cv2.cvtColor(aligned_bgr, cv2.COLOR_BGR2RGB).astype(np.float32)

        if self.norm_type == "insightface":
            # Standard InsightFace / ArcFace normalization: (x - 127.5) / 128.0
            norm = (rgb - 127.5) / 128.0
        elif self.norm_type == "zero_to_one":
            norm = rgb / 255.0
        elif self.norm_type == "standardize":
            mean, std = np.mean(rgb), np.std(rgb)
            std_adj = np.maximum(std, 1.0 / np.sqrt(rgb.size))
            norm = (rgb - mean) / std_adj
        else:
            norm = (rgb - 127.5) / 128.0

        blob = np.transpose(norm, (2, 0, 1))[None, ...]
        return blob.astype(np.float32)

    def extract_embedding(self, aligned_bgr: np.ndarray) -> np.ndarray:
        """
        Extract L2-normalized embedding vector from aligned face.
        """
        blob = self.preprocess(aligned_bgr)
        raw_emb = self.session.run(None, {self.input_name: blob})[0].flatten()

        # L2 Normalization
        l2_norm = np.linalg.norm(raw_emb)
        if l2_norm > 1e-7:
            emb = raw_emb / l2_norm
        else:
            emb = raw_emb

        return emb.astype(np.float32)

    @staticmethod
    def compute_similarity(emb1: np.ndarray, emb2: np.ndarray) -> float:
        """
        Cosine similarity between two L2-normalized embeddings.
        Ranges from -1.0 to 1.0 (typically 0.40+ indicates the same person).
        """
        return float(np.dot(emb1, emb2))

    @staticmethod
    def compute_distance(emb1: np.ndarray, emb2: np.ndarray) -> float:
        """
        Euclidean distance between two L2-normalized embeddings.
        Ranges from 0.0 to 2.0 (lower means closer match).
        """
        return float(np.linalg.norm(emb1 - emb2))


# Backward compatibility alias
ArcFaceRecognizer = FaceRecognizer
