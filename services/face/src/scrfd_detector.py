"""
SCRFD Face Detector module using ONNX Runtime.
Decodes multi-stride (8, 16, 32) anchor-free outputs with 5-point landmarks.
Compatible with InsightFace SCRFD models (e.g. SCRFD_10G_KPS, det_10g).
"""

import os
import logging
from typing import List, Tuple, Optional
import cv2
import numpy as np
import onnxruntime as ort

from .utils import nms

logger = logging.getLogger(__name__)


class SCRFDDetector:
    """
    ONNX Inference Engine for InsightFace SCRFD face detection models.

    SCRFD outputs 9 tensors (3 strides × 3 types):
      - scores:    [1, N, 1] per stride  (face confidence)
      - bboxes:    [1, N, 4] per stride  (distance to left, top, right, bottom)
      - landmarks: [1, N, 10] per stride (5 facial keypoints × 2 coords)

    Output order: score_8, score_16, score_32, bbox_8, bbox_16, bbox_32, kps_8, kps_16, kps_32
    """

    STRIDES = [8, 16, 32]
    NUM_ANCHORS = 2  # SCRFD uses 2 anchors per spatial location

    def __init__(
        self,
        model_path: str,
        input_size: int = 640,
        conf_threshold: float = 0.5,
        iou_threshold: float = 0.4,
        providers: Optional[List[str]] = None
    ):
        if not os.path.exists(model_path):
            raise FileNotFoundError(f"SCRFD model file not found: {model_path}")

        if providers is None:
            available = ort.get_available_providers()
            providers = (
                ['CUDAExecutionProvider', 'CPUExecutionProvider']
                if 'CUDAExecutionProvider' in available
                else ['CPUExecutionProvider']
            )

        self.input_size = input_size
        self.conf_threshold = conf_threshold
        self.iou_threshold = iou_threshold

        logger.info(f"Loading SCRFD Face Detector with providers: {providers}")
        self.session = ort.InferenceSession(model_path, providers=providers)
        self.input_name = self.session.get_inputs()[0].name

        # Detect whether this SCRFD model outputs landmarks
        num_outputs = len(self.session.get_outputs())
        self.has_landmarks = (num_outputs >= 9)
        logger.info(f"SCRFD model loaded: {num_outputs} outputs, landmarks={'yes' if self.has_landmarks else 'no'}")

        # Pre-generate anchor centers for the default input size
        self._anchor_cache = self._generate_anchors(self.input_size, self.input_size)

    def _generate_anchors(self, height: int, width: int) -> dict:
        """Generate anchor center points for each stride."""
        anchors = {}
        for stride in self.STRIDES:
            fh, fw = height // stride, width // stride
            shift_x = np.arange(0, fw) * stride
            shift_y = np.arange(0, fh) * stride
            xv, yv = np.meshgrid(shift_x, shift_y)
            pts = np.stack((xv.flatten(), yv.flatten()), axis=-1).astype(np.float32)
            # Duplicate for NUM_ANCHORS per location
            pts = np.stack([pts] * self.NUM_ANCHORS, axis=1).reshape(-1, 2)
            anchors[stride] = pts
        return anchors

    @staticmethod
    def _distance2bbox(points: np.ndarray, distance: np.ndarray) -> np.ndarray:
        """Decode distance predictions to bounding boxes [x1, y1, x2, y2]."""
        x1 = points[:, 0] - distance[:, 0]
        y1 = points[:, 1] - distance[:, 1]
        x2 = points[:, 0] + distance[:, 2]
        y2 = points[:, 1] + distance[:, 3]
        return np.stack([x1, y1, x2, y2], axis=-1)

    @staticmethod
    def _distance2kps(points: np.ndarray, distance: np.ndarray) -> np.ndarray:
        """Decode distance predictions to 5 facial landmarks (shape: N×5×2)."""
        num_pts = distance.shape[1] // 2
        kps = np.zeros((len(points), num_pts, 2), dtype=np.float32)
        for i in range(num_pts):
            kps[:, i, 0] = points[:, 0] + distance[:, 2 * i]
            kps[:, i, 1] = points[:, 1] + distance[:, 2 * i + 1]
        return kps

    def _preprocess(self, img_bgr: np.ndarray) -> Tuple[np.ndarray, float, Tuple[float, float]]:
        """
        Resize image to input_size×input_size with letterbox padding.
        Returns (blob, scale_ratio, (pad_w, pad_h)).
        """
        h0, w0 = img_bgr.shape[:2]
        target = self.input_size
        r = min(target / h0, target / w0)
        new_w, new_h = int(round(w0 * r)), int(round(h0 * r))
        dw = (target - new_w) / 2.0
        dh = (target - new_h) / 2.0

        if (w0, h0) != (new_w, new_h):
            resized = cv2.resize(img_bgr, (new_w, new_h), interpolation=cv2.INTER_LINEAR)
        else:
            resized = img_bgr.copy()

        top, bottom = int(round(dh - 0.1)), int(round(dh + 0.1))
        left, right = int(round(dw - 0.1)), int(round(dw + 0.1))
        padded = cv2.copyMakeBorder(resized, top, bottom, left, right,
                                    cv2.BORDER_CONSTANT, value=(0, 0, 0))

        # Standard InsightFace preprocessing:
        # Convert BGR to RGB and normalize via (x - 127.5) / 128.0
        rgb = cv2.cvtColor(padded, cv2.COLOR_BGR2RGB).astype(np.float32)
        norm = (rgb - 127.5) / 128.0
        blob = np.transpose(norm, (2, 0, 1))[None, ...].astype(np.float32)
        return blob, r, (dw, dh)

    def detect(
        self,
        img_bgr: np.ndarray,
        conf_threshold: Optional[float] = None,
        iou_threshold: Optional[float] = None
    ) -> Tuple[np.ndarray, np.ndarray, Optional[np.ndarray]]:
        """
        Detect faces in input image.

        Args:
            img_bgr: BGR input image (H, W, C).
            conf_threshold: Optional override for confidence threshold.
            iou_threshold: Optional override for NMS IoU threshold.

        Returns:
            boxes: np.ndarray of shape (N, 4) in [x1, y1, x2, y2] original image coordinates.
            scores: np.ndarray of shape (N,) containing detection confidences.
            landmarks: np.ndarray of shape (N, 5, 2) with 5-point facial landmarks, or None.
        """
        if img_bgr is None or img_bgr.size == 0:
            raise ValueError("Invalid input image: array is empty or None")

        effective_conf = conf_threshold if conf_threshold is not None else self.conf_threshold
        effective_iou = iou_threshold if iou_threshold is not None else self.iou_threshold

        orig_h, orig_w = img_bgr.shape[:2]

        # 1. Preprocess
        blob, ratio, (pad_w, pad_h) = self._preprocess(img_bgr)

        # 2. Inference
        outputs = self.session.run(None, {self.input_name: blob})

        # 3. Decode outputs across strides
        all_boxes: List[np.ndarray] = []
        all_scores: List[np.ndarray] = []
        all_kps: List[np.ndarray] = []

        num_strides = len(self.STRIDES)
        for i, stride in enumerate(self.STRIDES):
            # Output order: scores first, then bboxes, then landmarks
            score_raw = outputs[i].reshape(-1)
            bbox_raw = outputs[i + num_strides].reshape(-1, 4)

            # Filter by confidence
            valid = np.where(score_raw > effective_conf)[0]
            if len(valid) == 0:
                continue

            scores = score_raw[valid]

            # Get anchor points for this stride
            anchor_pts = self._anchor_cache[stride][valid]

            # Decode bboxes: distances are in stride units, multiply by stride
            decoded_boxes = self._distance2bbox(anchor_pts, bbox_raw[valid] * stride)

            all_boxes.append(decoded_boxes)
            all_scores.append(scores)

            # Decode landmarks if available
            if self.has_landmarks:
                kps_raw = outputs[i + 2 * num_strides].reshape(-1, 10)
                decoded_kps = self._distance2kps(anchor_pts, kps_raw[valid] * stride)
                all_kps.append(decoded_kps)

        if not all_boxes:
            empty_boxes = np.empty((0, 4), dtype=np.float32)
            empty_scores = np.empty((0,), dtype=np.float32)
            empty_kps = np.empty((0, 5, 2), dtype=np.float32) if self.has_landmarks else None
            return empty_boxes, empty_scores, empty_kps

        all_boxes_arr = np.vstack(all_boxes).astype(np.float32)
        all_scores_arr = np.concatenate(all_scores).astype(np.float32)

        # 4. Rescale from letterboxed coordinates to original image space
        all_boxes_arr[:, [0, 2]] = np.clip((all_boxes_arr[:, [0, 2]] - pad_w) / ratio, 0, orig_w - 1)
        all_boxes_arr[:, [1, 3]] = np.clip((all_boxes_arr[:, [1, 3]] - pad_h) / ratio, 0, orig_h - 1)

        landmarks_arr = None
        if self.has_landmarks and all_kps:
            landmarks_arr = np.vstack(all_kps).astype(np.float32)
            landmarks_arr[:, :, 0] = np.clip((landmarks_arr[:, :, 0] - pad_w) / ratio, 0, orig_w - 1)
            landmarks_arr[:, :, 1] = np.clip((landmarks_arr[:, :, 1] - pad_h) / ratio, 0, orig_h - 1)

        # 5. NMS
        keep = nms(all_boxes_arr, all_scores_arr, effective_iou)
        boxes_out = all_boxes_arr[keep]
        scores_out = all_scores_arr[keep]
        kps_out = landmarks_arr[keep] if landmarks_arr is not None else None

        return boxes_out, scores_out, kps_out

