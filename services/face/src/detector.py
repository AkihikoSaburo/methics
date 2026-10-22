"""
YOLO-FaceV2 Face Detector module using ONNX Runtime.
"""

import os
import logging
from typing import List, Tuple, Optional
import cv2
import numpy as np
import onnxruntime as ort

from .utils import letterbox, nms, sigmoid

logger = logging.getLogger(__name__)


class YOLOFaceDetector:
    """
    ONNX Inference Engine for YOLO-FaceV2 models (yolo-facev2s / yolo-facev2m).
    Decodes multi-scale feature maps from 3 strides (8, 16, 32) using ERF anchors.
    """

    def __init__(
        self,
        model_path: str,
        conf_threshold: float = 0.35,
        iou_threshold: float = 0.45,
        providers: Optional[List[str]] = None
    ):
        if not os.path.exists(model_path):
            raise FileNotFoundError(f"Detector model file not found: {model_path}")

        if providers is None:
            available = ort.get_available_providers()
            providers = (
                ['CUDAExecutionProvider', 'CPUExecutionProvider']
                if 'CUDAExecutionProvider' in available
                else ['CPUExecutionProvider']
            )

        logger.info(f"Loading YOLO Face Detector with providers: {providers}")
        self.session = ort.InferenceSession(model_path, providers=providers)
        self.input_name = self.session.get_inputs()[0].name
        self.conf_threshold = conf_threshold
        self.iou_threshold = iou_threshold

        # Standard YOLOv5 anchors corresponding to strides [8, 16, 32]
        self.anchors = [
            np.array([[10, 13], [16, 30], [33, 23]], dtype=np.float32),      # P3 / stride 8
            np.array([[30, 61], [62, 45], [59, 119]], dtype=np.float32),     # P4 / stride 16
            np.array([[116, 90], [156, 198], [373, 326]], dtype=np.float32), # P5 / stride 32
        ]
        self.strides = [8, 16, 32]

    def detect(
        self,
        img_bgr: np.ndarray,
        conf_threshold: Optional[float] = None,
        iou_threshold: Optional[float] = None
    ) -> Tuple[np.ndarray, np.ndarray]:
        """
        Detect faces in input image.

        Args:
            img_bgr: BGR input image (H, W, C).
            conf_threshold: Optional override for confidence threshold.
            iou_threshold: Optional override for NMS IoU threshold.

        Returns:
            boxes: np.ndarray of shape (N, 4) in [x1, y1, x2, y2] coordinates.
            scores: np.ndarray of shape (N,) containing detection confidences.
        """
        if img_bgr is None or img_bgr.size == 0:
            raise ValueError("Invalid input image: array is empty or None")

        effective_conf = conf_threshold if conf_threshold is not None else self.conf_threshold
        effective_iou = iou_threshold if iou_threshold is not None else self.iou_threshold

        orig_h, orig_w = img_bgr.shape[:2]

        # 1. Letterbox resize to 640x640
        lb_img, ratio, (pad_w, pad_h) = letterbox(img_bgr, (640, 640))

        # 2. Normalize and transpose: (640, 640, 3) -> (1, 3, 640, 640)
        blob = cv2.cvtColor(lb_img, cv2.COLOR_BGR2RGB).astype(np.float32) / 255.0
        blob = np.transpose(blob, (2, 0, 1))[None, ...]

        # 3. Inference
        outputs = self.session.run(None, {self.input_name: blob})

        # 4. Decode outputs across the 3 heads
        all_boxes_list: List[np.ndarray] = []
        all_scores_list: List[np.ndarray] = []

        for out in outputs:
            bs, na, ny, nx, no = out.shape
            stride = 640 // ny
            if stride not in self.strides:
                logger.warning(f"Unexpected stride {stride} encountered during detection decoding.")
                continue

            anchor = self.anchors[self.strides.index(stride)].reshape(1, na, 1, 1, 2)

            if out.min() < 0 or out.max() > 1:  # Logits check
                out = sigmoid(out)

            yv, xv = np.meshgrid(np.arange(ny), np.arange(nx), indexing='ij')
            grid = np.stack((xv, yv), axis=2).reshape(1, 1, ny, nx, 2).astype(np.float32)

            xy = (out[..., 0:2] * 2.0 - 0.5 + grid) * stride
            wh = (out[..., 2:4] * 2.0) ** 2 * anchor
            scores = out[..., 4:5] * out[..., 5:6]

            mask = (scores >= effective_conf)[..., 0]
            if np.any(mask):
                sel_xy = xy[mask]
                sel_wh = wh[mask]
                sel_scores = scores[mask, 0]

                # Convert cx, cy, w, h -> x1, y1, x2, y2
                x1 = sel_xy[:, 0] - sel_wh[:, 0] / 2.0
                y1 = sel_xy[:, 1] - sel_wh[:, 1] / 2.0
                x2 = sel_xy[:, 0] + sel_wh[:, 0] / 2.0
                y2 = sel_xy[:, 1] + sel_wh[:, 1] / 2.0

                # Rescale and unpad back to original image space
                x1 = np.clip((x1 - pad_w) / ratio, 0, orig_w - 1)
                y1 = np.clip((y1 - pad_h) / ratio, 0, orig_h - 1)
                x2 = np.clip((x2 - pad_w) / ratio, 0, orig_w - 1)
                y2 = np.clip((y2 - pad_h) / ratio, 0, orig_h - 1)

                head_boxes = np.stack([x1, y1, x2, y2], axis=1)
                all_boxes_list.append(head_boxes)
                all_scores_list.append(sel_scores)

        if not all_boxes_list:
            return np.empty((0, 4), dtype=np.float32), np.empty((0,), dtype=np.float32)

        all_boxes = np.vstack(all_boxes_list).astype(np.float32)
        all_scores = np.concatenate(all_scores_list).astype(np.float32)

        # 5. Non-Maximum Suppression (NMS)
        keep = nms(all_boxes, all_scores, effective_iou)
        boxes, scores_out = all_boxes[keep], all_scores[keep]

        # 6. Suppress contained / heavily overlapping detections
        keep2 = self._suppress_contained(boxes, scores_out, thr=0.7)
        return boxes[keep2], scores_out[keep2]

    @staticmethod
    def _suppress_contained(
        boxes: np.ndarray,
        scores: np.ndarray,
        thr: float = 0.7
    ) -> List[int]:
        """
        Remove detections that are largely contained within a higher-confidence
        detection. This catches residual double-detections that survive NMS
        when one box is much smaller than the other.
        """
        if len(boxes) == 0:
            return []

        order = np.argsort(-scores)
        keep: List[int] = []

        for idx in order:
            ax1, ay1, ax2, ay2 = boxes[idx]
            area_a = (ax2 - ax1) * (ay2 - ay1)
            drop = False
            for k_idx in keep:
                kx1, ky1, kx2, ky2 = boxes[k_idx]
                ix1 = max(ax1, kx1)
                iy1 = max(ay1, ky1)
                ix2 = min(ax2, kx2)
                iy2 = min(ay2, ky2)
                inter = max(0.0, ix2 - ix1) * max(0.0, iy2 - iy1)
                area_k = (kx2 - kx1) * (ky2 - ky1)
                if inter / max(min(area_a, area_k), 1.0) > thr:
                    drop = True
                    break
            if not drop:
                keep.append(int(idx))

        return keep
