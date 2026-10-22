"""
Utility functions and geometric operations for Face Detection and Recognition.
"""

from typing import List, Tuple, Sequence
import cv2
import numpy as np


# ---------------------------------------------------------
# Standard InsightFace / ArcFace 112x112 reference landmarks
# ---------------------------------------------------------
ARCFACE_DST_112 = np.array([
    [38.2946, 51.6963],  # left eye
    [73.5318, 51.5014],  # right eye
    [56.0252, 71.7366],  # nose tip
    [41.5493, 92.3655],  # mouth left
    [70.7299, 92.2041]   # mouth right
], dtype=np.float32)


def sigmoid(x: np.ndarray) -> np.ndarray:
    """Numerically stable sigmoid function."""
    return 1.0 / (1.0 + np.exp(-np.clip(x, -50.0, 50.0)))


def letterbox(
    img: np.ndarray,
    new_shape: Tuple[int, int] = (640, 640),
    color: Tuple[int, int, int] = (114, 114, 114)
) -> Tuple[np.ndarray, float, Tuple[float, float]]:
    """
    Resize image to new_shape with aspect ratio preservation and padding.
    
    Args:
        img: Input BGR image (H, W, C).
        new_shape: Target dimensions (height, width).
        color: Padding fill color in BGR.

    Returns:
        padded_img, scale_ratio, (pad_w, pad_h)
    """
    h0, w0 = img.shape[:2]
    r = min(new_shape[0] / h0, new_shape[1] / w0)
    new_unpad = int(round(w0 * r)), int(round(h0 * r))
    dw, dh = new_shape[1] - new_unpad[0], new_shape[0] - new_unpad[1]
    dw /= 2.0
    dh /= 2.0

    if (w0, h0) != new_unpad:
        img_resized = cv2.resize(img, new_unpad, interpolation=cv2.INTER_LINEAR)
    else:
        img_resized = img.copy()

    top, bottom = int(round(dh - 0.1)), int(round(dh + 0.1))
    left, right = int(round(dw - 0.1)), int(round(dw + 0.1))

    img_padded = cv2.copyMakeBorder(
        img_resized, top, bottom, left, right, cv2.BORDER_CONSTANT, value=color
    )
    return img_padded, r, (dw, dh)


def nms(boxes: np.ndarray, scores: np.ndarray, iou_threshold: float = 0.45) -> List[int]:
    """
    Non-Maximum Suppression (NMS) in pure NumPy.

    Args:
        boxes: Array of bounding boxes with shape (N, 4) in [x1, y1, x2, y2].
        scores: Array of confidence scores with shape (N,).
        iou_threshold: Intersection-over-Union threshold for suppression.

    Returns:
        List of surviving box indices.
    """
    if len(boxes) == 0:
        return []

    x1 = boxes[:, 0]
    y1 = boxes[:, 1]
    x2 = boxes[:, 2]
    y2 = boxes[:, 3]
    areas = (x2 - x1) * (y2 - y1)
    order = scores.argsort()[::-1]

    keep: List[int] = []
    while order.size > 0:
        i = order[0]
        keep.append(i)
        if order.size == 1:
            break

        xx1 = np.maximum(x1[i], x1[order[1:]])
        yy1 = np.maximum(y1[i], y1[order[1:]])
        xx2 = np.minimum(x2[i], x2[order[1:]])
        yy2 = np.minimum(y2[i], y2[order[1:]])

        w = np.maximum(0.0, xx2 - xx1)
        h = np.maximum(0.0, yy2 - yy1)
        inter = w * h

        ovr = inter / (areas[i] + areas[order[1:]] - inter + 1e-7)
        inds = np.where(ovr <= iou_threshold)[0]
        order = order[inds + 1]

    return keep


def draw_detection(
    img: np.ndarray,
    bbox: Sequence[float],
    confidence: float,
    label: str = "Face",
    color: Tuple[int, int, int] = (0, 255, 0),
    thickness: int = 2
) -> None:
    """
    Draw bounding box and confidence label onto an image in-place.
    """
    x1, y1, x2, y2 = [int(v) for v in bbox]
    cv2.rectangle(img, (x1, y1), (x2, y2), color, thickness)
    text = f"{label} {confidence:.2f}"
    text_pos = (x1, max(15, y1 - 8))
    cv2.putText(
        img,
        text,
        text_pos,
        cv2.FONT_HERSHEY_SIMPLEX,
        0.6,
        color,
        thickness
    )


def draw_overlay(
    frame: np.ndarray,
    crop: np.ndarray,
    top: int = 10,
    margin_right: int = 10,
    border_color: Tuple[int, int, int] = (255, 255, 255)
) -> None:
    """
    Draw an overlay thumbnail (e.g. aligned face crop) onto frame in-place.
    """
    h_f, w_f = frame.shape[:2]
    h_c, w_c = crop.shape[:2]
    if w_f >= w_c + margin_right + 10 and h_f >= h_c + top + 10:
        x_start = w_f - w_c - margin_right
        x_end = w_f - margin_right
        y_start = top
        y_end = top + h_c
        frame[y_start:y_end, x_start:x_end] = crop
        cv2.rectangle(frame, (x_start, y_start), (x_end, y_end), border_color, 1)
