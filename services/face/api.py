"""
FastAPI Face Recognition API
=============================
REST API endpoints for YOLO-FaceV2 face detection and ArcFace face recognition.
Integrated with Hugging Face Hub for real-time model cataloging & on-demand downloads.

Endpoints:
  POST /detect           — Detect faces in an image, return bounding boxes + embeddings
  POST /compare          — Compare two face images, return similarity score
  POST /embed            — Extract 512-d face embedding from an image
  POST /detect/visualize  — Detect faces and return the annotated JPEG image
  GET  /models           — List available models from Hugging Face Hub and local status
  POST /models/download  — Download a model from Hugging Face Hub
  POST /models/switch    — Dynamically switch active detection / recognition models
  GET  /health           — Health check / readiness probe
"""

import io
import time
import base64
import logging
from contextlib import asynccontextmanager
from typing import List, Optional, Dict, Any

import cv2
import numpy as np
from fastapi import FastAPI, File, UploadFile, HTTPException, Query, status
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import StreamingResponse
from pydantic import BaseModel, Field

from src import FacePipeline, get_pipeline, settings, get_model_manager

logger = logging.getLogger(__name__)

pipeline: Optional[FacePipeline] = None
start_time: float = 0.0


# ---------------------------------------------------------------------------
# Lifespan: load models at startup
# ---------------------------------------------------------------------------
@asynccontextmanager
async def lifespan(app: FastAPI):
    global pipeline, start_time
    logging.basicConfig(
        level=getattr(logging, settings.log_level, logging.INFO),
        format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
    )
    logger.info("Initializing Face Recognition Pipeline models...")
    start_time = time.time()
    try:
        pipeline = get_pipeline(
            det_model_path=settings.det_model_path,
            rec_model_path=settings.rec_model_path,
            conf_threshold=settings.conf_threshold,
            iou_threshold=settings.iou_threshold,
        )
        logger.info(f"Models successfully loaded in {time.time() - start_time:.2f}s")
    except Exception as exc:
        logger.exception("Failed to load models during startup")
        raise exc

    yield
    logger.info("Shutting down Face Recognition API...")


# ---------------------------------------------------------------------------
# FastAPI app
# ---------------------------------------------------------------------------
app = FastAPI(
    title="Face Recognition API",
    description=(
        "Production-ready REST API for face detection and face recognition with "
        "Hugging Face Hub model auto-downloading and runtime model switching."
    ),
    version="1.1.0",
    lifespan=lifespan,
)

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)


# ---------------------------------------------------------------------------
# Pydantic Schemas
# ---------------------------------------------------------------------------
class BBox(BaseModel):
    x1: float
    y1: float
    x2: float
    y2: float


class DetectedFace(BaseModel):
    bbox: BBox
    confidence: float
    embedding: List[float] = Field(
        description="L2-normalized face embedding vector"
    )
    aligned_face_b64: Optional[str] = Field(
        None, description="Base64 encoded JPEG crop of the aligned face (if requested)"
    )


class DetectResponse(BaseModel):
    num_faces: int
    faces: List[DetectedFace]
    processing_time_ms: float


class CompareResponse(BaseModel):
    cosine_similarity: float = Field(
        description="Cosine similarity between 2 faces: [-1.0, 1.0]"
    )
    euclidean_distance: float = Field(
        description="Euclidean distance between 2 normalized embeddings: [0.0, 2.0]"
    )
    threshold: float
    match: bool
    face1_confidence: float
    face2_confidence: float
    face1_aligned_b64: Optional[str] = None
    face2_aligned_b64: Optional[str] = None
    processing_time_ms: float


class EmbedResponse(BaseModel):
    embedding: List[float]
    confidence: float
    processing_time_ms: float


class HealthResponse(BaseModel):
    status: str
    uptime_seconds: float
    model_loaded: bool
    det_model: str
    rec_model: str


class DownloadModelRequest(BaseModel):
    model_id: str = Field(..., description="Model ID to download from Hugging Face (e.g. face_detection/yolo-facev2m.onnx)")
    force: bool = Field(False, description="Force re-download even if already downloaded")


class SwitchModelRequest(BaseModel):
    det_model: Optional[str] = Field(None, description="Detection model ID or relative path")
    rec_model: Optional[str] = Field(None, description="Recognition model ID or relative path")
    conf_threshold: Optional[float] = None
    iou_threshold: Optional[float] = None


# ---------------------------------------------------------------------------
# Helper Functions
# ---------------------------------------------------------------------------
def _decode_image(file_bytes: bytes) -> np.ndarray:
    """Decode uploaded bytes into a BGR OpenCV image."""
    if not file_bytes:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="Empty image file received.",
        )
    buf = np.frombuffer(file_bytes, dtype=np.uint8)
    img = cv2.imdecode(buf, cv2.IMREAD_COLOR)
    if img is None:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="Cannot decode image. Ensure the file is a valid JPEG/PNG image.",
        )
    return img


def _encode_aligned(aligned_bgr: np.ndarray) -> str:
    """Encode an aligned face crop to base64 JPEG string."""
    _, buf = cv2.imencode(".jpg", aligned_bgr, [cv2.IMWRITE_JPEG_QUALITY, 95])
    return base64.b64encode(buf.tobytes()).decode("ascii")


def _get_active_pipeline() -> FacePipeline:
    """Retrieve initialized pipeline or raise 503 error."""
    if pipeline is None:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="Face Recognition models are not initialized.",
        )
    return pipeline


# ---------------------------------------------------------------------------
# Endpoints: System & Models
# ---------------------------------------------------------------------------
@app.get("/health", response_model=HealthResponse, tags=["System"])
async def health_check():
    """Health check / readiness probe."""
    is_ready = pipeline is not None
    uptime = time.time() - start_time if start_time > 0 else 0.0
    det_m = pipeline.det_model_path if pipeline else settings.det_model_path
    rec_m = pipeline.rec_model_path if pipeline else settings.rec_model_path
    return HealthResponse(
        status="ok" if is_ready else "initializing",
        uptime_seconds=round(uptime, 2),
        model_loaded=is_ready,
        det_model=det_m,
        rec_model=rec_m,
    )


@app.get("/models", tags=["Model Management"])
async def list_models(refresh_remote: bool = Query(True, description="Query Hugging Face Hub live")):
    """
    List available face detection and recognition models from Hugging Face Hub.
    Returns download status, file sizes, configurations, and defaults.
    """
    mgr = get_model_manager()
    return mgr.list_available_models(refresh_remote=refresh_remote)


@app.post("/models/download", tags=["Model Management"])
async def download_model(req: DownloadModelRequest):
    """
    Explicitly download a model from Hugging Face Hub to local disk.
    """
    mgr = get_model_manager()
    try:
        path = mgr.ensure_model(req.model_id, force_download=req.force)
        return {
            "status": "success",
            "model_id": req.model_id,
            "path": path,
            "message": "Model downloaded and ready."
        }
    except Exception as e:
        raise HTTPException(status_code=400, detail=str(e))


@app.post("/models/switch", tags=["Model Management"])
async def switch_pipeline_models(req: SwitchModelRequest):
    """
    Dynamically reload the active detection / recognition models in memory.
    """
    global pipeline
    try:
        current_det = pipeline.det_model_path if pipeline else settings.det_model_path
        current_rec = pipeline.rec_model_path if pipeline else settings.rec_model_path

        new_det = req.det_model or current_det
        new_rec = req.rec_model or current_rec

        pipeline = get_pipeline(
            det_model_path=new_det,
            rec_model_path=new_rec,
            conf_threshold=req.conf_threshold,
            iou_threshold=req.iou_threshold,
            force_reload=True
        )
        return {
            "status": "success",
            "active_det_model": pipeline.det_model_path,
            "active_rec_model": pipeline.rec_model_path,
            "message": "Pipeline successfully switched to new models."
        }
    except Exception as e:
        raise HTTPException(status_code=400, detail=str(e))


# ---------------------------------------------------------------------------
# Endpoints: Face Detection & Recognition
# ---------------------------------------------------------------------------
@app.post("/detect", response_model=DetectResponse, tags=["Face Detection"])
async def detect_faces(
    image: UploadFile = File(..., description="Image file (JPEG/PNG)"),
    return_aligned: bool = Query(
        False, description="Include base64-encoded aligned face crops in response"
    ),
    conf_threshold: Optional[float] = Query(
        None, ge=0.0, le=1.0, description="Override confidence threshold (0.0–1.0)"
    ),
    iou_threshold: Optional[float] = Query(
        None, ge=0.0, le=1.0, description="Override NMS IoU threshold (0.0–1.0)"
    ),
):
    """
    Detect all faces in an uploaded image.
    Returns bounding boxes, confidence scores, and embeddings.
    """
    pipe = _get_active_pipeline()
    file_bytes = await image.read()
    img_bgr = _decode_image(file_bytes)

    t0 = time.time()
    faces = pipe.process_image(
        img_bgr,
        conf_threshold=conf_threshold,
        iou_threshold=iou_threshold
    )
    elapsed_ms = (time.time() - t0) * 1000

    detected_faces = []
    for f in faces:
        bbox_data = f["bbox"]
        aligned_b64 = _encode_aligned(f["aligned_face"]) if return_aligned else None

        detected_faces.append(
            DetectedFace(
                bbox=BBox(
                    x1=bbox_data[0],
                    y1=bbox_data[1],
                    x2=bbox_data[2],
                    y2=bbox_data[3],
                ),
                confidence=f["confidence"],
                embedding=f["embedding"].tolist(),
                aligned_face_b64=aligned_b64,
            )
        )

    return DetectResponse(
        num_faces=len(detected_faces),
        faces=detected_faces,
        processing_time_ms=round(elapsed_ms, 2),
    )


@app.post("/compare", response_model=CompareResponse, tags=["Face Recognition"])
async def compare_faces(
    image1: UploadFile = File(..., description="First face image"),
    image2: UploadFile = File(..., description="Second face image"),
    sim_threshold: Optional[float] = Query(
        None,
        ge=0.0,
        le=1.0,
        description="Cosine similarity threshold to declare a match",
    ),
    conf_threshold: Optional[float] = Query(
        None, ge=0.0, le=1.0, description="Detection confidence threshold"
    ),
    return_aligned: bool = Query(
        False, description="Include base64-encoded aligned face crops in response"
    ),
):
    """
    Compare the most prominent face from two images.
    Returns cosine similarity, euclidean distance, and match result.
    """
    pipe = _get_active_pipeline()
    bytes1 = await image1.read()
    bytes2 = await image2.read()
    img1 = _decode_image(bytes1)
    img2 = _decode_image(bytes2)

    t0 = time.time()
    result = pipe.compare_faces(
        img1,
        img2,
        similarity_threshold=sim_threshold,
        conf_threshold=conf_threshold
    )
    elapsed_ms = (time.time() - t0) * 1000

    if "error" in result:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
            detail=result["error"]
        )

    return CompareResponse(
        cosine_similarity=result["similarity"],
        euclidean_distance=result["euclidean_distance"],
        threshold=result["threshold"],
        match=result["match"],
        face1_confidence=result["face1_confidence"],
        face2_confidence=result["face2_confidence"],
        face1_aligned_b64=(
            _encode_aligned(result["face1_aligned"]) if return_aligned else None
        ),
        face2_aligned_b64=(
            _encode_aligned(result["face2_aligned"]) if return_aligned else None
        ),
        processing_time_ms=round(elapsed_ms, 2),
    )


@app.post("/embed", response_model=EmbedResponse, tags=["Embedding"])
async def extract_embedding(
    image: UploadFile = File(..., description="Image file containing a face"),
    conf_threshold: Optional[float] = Query(
        None, ge=0.0, le=1.0, description="Confidence threshold"
    ),
):
    """
    Extract face embedding from the most prominent face in the image.
    """
    pipe = _get_active_pipeline()
    file_bytes = await image.read()
    img_bgr = _decode_image(file_bytes)

    t0 = time.time()
    faces = pipe.process_image(img_bgr, conf_threshold=conf_threshold)
    elapsed_ms = (time.time() - t0) * 1000

    if not faces:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
            detail="No face detected in the uploaded image",
        )

    best = max(faces, key=lambda f: f["confidence"])
    return EmbedResponse(
        embedding=best["embedding"].tolist(),
        confidence=best["confidence"],
        processing_time_ms=round(elapsed_ms, 2),
    )


@app.post("/detect/visualize", tags=["Face Detection"])
async def detect_and_visualize(
    image: UploadFile = File(..., description="Image file (JPEG/PNG)"),
    conf_threshold: Optional[float] = Query(
        None, ge=0.0, le=1.0, description="Confidence threshold"
    ),
):
    """
    Detect faces and return the annotated JPEG image with bounding boxes drawn.
    """
    pipe = _get_active_pipeline()
    file_bytes = await image.read()
    img_bgr = _decode_image(file_bytes)

    faces = pipe.process_image(img_bgr, conf_threshold=conf_threshold)

    viz = img_bgr.copy()
    for f in faces:
        x1, y1, x2, y2 = [int(v) for v in f["bbox"]]
        conf = f["confidence"]
        cv2.rectangle(viz, (x1, y1), (x2, y2), (0, 255, 0), 2)
        cv2.putText(
            viz,
            f"Face {conf:.2f}",
            (x1, max(15, y1 - 8)),
            cv2.FONT_HERSHEY_SIMPLEX,
            0.6,
            (0, 255, 0),
            2,
        )

    _, buf = cv2.imencode(".jpg", viz, [cv2.IMWRITE_JPEG_QUALITY, 95])
    return StreamingResponse(
        io.BytesIO(buf.tobytes()),
        media_type="image/jpeg",
        headers={"Content-Disposition": "inline; filename=detected.jpg"},
    )
