"""
FastAPI Face Recognition API
=============================
REST API endpoints for YOLO-FaceV2 face detection and ArcFace face recognition.
Integrated with Hugging Face Hub for real-time model cataloging & on-demand downloads.
Designed as a microservice backend for an attendance system.

Endpoints:
  POST /embed            — Extract 512-d face embedding from an image
  POST /register         — Register a student with up to 6 face templates (enrollment)
  GET  /models           — List available models from Hugging Face Hub and local status
  POST /models/download  — Download a model from Hugging Face Hub
  POST /models/switch    — Dynamically switch active detection / recognition models
  GET  /health           — Health check / readiness probe
"""


import time
import base64
import logging
from contextlib import asynccontextmanager
from typing import List, Optional, Dict, Any

import cv2
import numpy as np
from fastapi import FastAPI, File, UploadFile, HTTPException, Query, status
from fastapi.middleware.cors import CORSMiddleware

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
# Endpoints: Face Recognition & Embedding
# ---------------------------------------------------------------------------
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



# ---------------------------------------------------------------------------
# Endpoint: Student Registration (Bulk Template Extraction)
# ---------------------------------------------------------------------------
class TemplateEmbedding(BaseModel):
    """Embedding (and optional aligned crop) for a single registration photo."""
    embedding: List[float] = Field(
        ..., description="512-dim L2-normalized face vector"
    )
    confidence: float = Field(
        ..., description="Detection confidence of the chosen face"
    )
    aligned_face_b64: Optional[str] = Field(
        None, description="Base64 JPEG of the aligned face crop (if requested)"
    )


class RegisterResponse(BaseModel):
    """Response returned after uploading 1-6 registration photos."""
    student_id: str = Field(
        ..., description="Unique identifier of the student"
    )
    num_templates: int = Field(
        ..., description="Number of templates successfully extracted"
    )
    templates: List[TemplateEmbedding] = Field(
        ..., description="One entry per uploaded image (max 6)"
    )
    processing_time_ms: float = Field(
        ..., description="Total time taken for all images"
    )


@app.post(
    "/register",
    response_model=RegisterResponse,
    tags=["Enrollment"],
)
async def register_student(
    student_id: str = Query(
        ..., description="Student identifier (e.g. NIM)"
    ),
    images: List[UploadFile] = File(
        ...,
        description=(
            "1-6 face images for the student in different poses "
            "(left, right, top, bottom, front, smile)"
        ),
    ),
    return_aligned: bool = Query(
        False,
        description="If true, include base64-encoded aligned face crops in response",
    ),
    conf_threshold: Optional[float] = Query(
        None, ge=0.0, le=1.0, description="Override detection confidence threshold"
    ),
):
    """
    Register a student by uploading up to 6 face photos (different poses).

    The server extracts a 512-dim ArcFace embedding for each photo.
    Store the returned embeddings in your student-template database
    and use them later with /embed for attendance verification.

    Recommended poses:
    1. Front (straight ahead)
    2. Left (turned ~30° left)
    3. Right (turned ~30° right)
    4. Top (head tilted slightly up)
    5. Bottom (head tilted slightly down)
    6. Smile (natural smile, front-facing)
    """
    if len(images) == 0:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="At least one image must be provided.",
        )
    if len(images) > 6:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="Maximum of 6 registration images are allowed per request.",
        )

    pipe = _get_active_pipeline()
    start = time.time()
    templates: List[TemplateEmbedding] = []

    for idx, img_file in enumerate(images, start=1):
        img_bytes = await img_file.read()
        img_bgr = _decode_image(img_bytes)

        faces = pipe.process_image(img_bgr, conf_threshold=conf_threshold)
        if not faces:
            raise HTTPException(
                status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
                detail=(
                    f"No face detected in image #{idx} "
                    f"('{img_file.filename}'). "
                    f"Ensure the photo contains a clearly visible face."
                ),
            )

        # Pick the most confident face (usually only one per registration photo)
        best = max(faces, key=lambda f: f["confidence"])
        aligned_b64 = _encode_aligned(best["aligned_face"]) if return_aligned else None

        templates.append(
            TemplateEmbedding(
                embedding=best["embedding"].tolist(),
                confidence=float(best["confidence"]),
                aligned_face_b64=aligned_b64,
            )
        )

    elapsed_ms = (time.time() - start) * 1000
    return RegisterResponse(
        student_id=student_id,
        num_templates=len(templates),
        templates=templates,
        processing_time_ms=round(elapsed_ms, 2),
    )
