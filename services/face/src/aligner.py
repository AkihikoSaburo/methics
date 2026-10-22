"""
Face Alignment module for ArcFace canonical space (112x112).
"""

from typing import Tuple, Optional
import cv2
import numpy as np

from .utils import ARCFACE_DST_112


class FaceAligner:
    """
    Performs face alignment to standard ArcFace 112x112 coordinate space.
    Supports:
    1. 5-point landmark similarity transformation (Umeyama / Affine)
    2. Canonical ArcFace bounding-box affine alignment (when landmarks are unavailable)
    """

    def __init__(self, output_size: Tuple[int, int] = (112, 112)):
        self.output_size = output_size
        self.reference_landmarks = ARCFACE_DST_112

    def align_by_landmarks(self, img_bgr: np.ndarray, landmarks_5pts: np.ndarray) -> np.ndarray:
        """
        Align face using 5 facial landmarks (left eye, right eye, nose, left mouth, right mouth)
        via similarity transformation matrix.
        """
        if img_bgr is None or img_bgr.size == 0:
            raise ValueError("Invalid input image: array is empty or None")

        if landmarks_5pts is None or landmarks_5pts.shape != (5, 2):
            raise ValueError(f"Landmarks must have shape (5, 2), got: {getattr(landmarks_5pts, 'shape', None)}")

        M, _ = cv2.estimateAffinePartial2D(landmarks_5pts.astype(np.float32), self.reference_landmarks)
        if M is None:
            # Fallback if matrix estimation fails
            return cv2.resize(img_bgr, self.output_size)

        aligned = cv2.warpAffine(img_bgr, M, self.output_size, borderMode=cv2.BORDER_CONSTANT, borderValue=0)
        return aligned

    def align_by_bbox(self, img_bgr: np.ndarray, bbox: np.ndarray, margin: float = 0.25) -> np.ndarray:
        """
        Align face using bounding box mapped into ArcFace 112x112 canonical proportions.

        Strategy: Create a square crop centered on the face region.
        The face center is estimated from the bbox, using the shorter side
        as the base dimension to form a square crop. A margin is then added
        for contextual padding (forehead/chin).

        In standard ArcFace 112x112 coordinate system:
          - Horizontal face span: ~ 16px to 96px (width ~ 80px, centered at x=56)
          - Vertical face span:   ~ 15px to 105px (height ~ 90px, center at y=60)
        """
        if img_bgr is None or img_bgr.size == 0:
            raise ValueError("Invalid input image: array is empty or None")

        if bbox is None or len(bbox) != 4:
            raise ValueError(f"Invalid bounding box: expected array of 4 coordinates, got {bbox}")

        x1, y1, x2, y2 = bbox
        w = max(1.0, float(x2 - x1))
        h = max(1.0, float(y2 - y1))

        # Use the shorter dimension as base for a square crop.
        # This avoids overly tall/wide bboxes from stretching the alignment.
        side = min(w, h)

        # If bbox aspect ratio is extreme (e.g. height >> width),
        # face is likely only in the upper portion.
        cx = (x1 + x2) / 2.0
        if h > w * 1.5:
            # Face is likely in upper ~60% of a tall bbox
            cy = y1 + w * 0.6
        else:
            cy = (y1 + y2) / 2.0

        # Apply margin to the square side
        half = side * (1.0 + margin) / 2.0

        # Source: square region around estimated face center
        src_pts = np.array([
            [cx - half, cy - half],
            [cx + half, cy - half],
            [cx - half, cy + half]
        ], dtype=np.float32)

        # Target coordinates inside 112x112 frame (ArcFace canonical)
        dst_pts = np.array([
            [16.0, 15.0],
            [96.0, 15.0],
            [16.0, 105.0]
        ], dtype=np.float32)

        M = cv2.getAffineTransform(src_pts, dst_pts)
        aligned = cv2.warpAffine(
            img_bgr,
            M,
            self.output_size,
            borderMode=cv2.BORDER_CONSTANT,
            borderValue=0
        )
        return aligned

    def align(
        self,
        img_bgr: np.ndarray,
        bbox: Optional[np.ndarray] = None,
        landmarks: Optional[np.ndarray] = None
    ) -> np.ndarray:
        """
        General alignment entrypoint: uses 5-point landmarks if available,
        otherwise performs canonical bounding-box affine alignment.
        """
        if landmarks is not None:
            return self.align_by_landmarks(img_bgr, landmarks)
        elif bbox is not None:
            return self.align_by_bbox(img_bgr, bbox)
        else:
            raise ValueError("Either bbox or landmarks must be provided for face alignment.")
