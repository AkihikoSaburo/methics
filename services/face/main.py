"""
Face Detection (YOLO-FaceV2) & Face Recognition (ArcFace) CLI & Runner
======================================================================
Features:
- Face Detection: Supports both yolo-facev2s.onnx and yolo-facev2m.onnx
- Face Alignment: Aligns face to ArcFace standard (112, 112) coordinate space
- Face Recognition: ArcFace ResNet50 (arcface_r50_fp16.onnx) for 512-d feature vectors
- Comparison, detection, video file processing, and live webcam modes.
- Hugging Face Hub model integration: Auto-downloads models on-demand.
"""

import os
import sys
import argparse
import logging
from typing import Tuple

import cv2
import numpy as np

from src import (
    FacePipeline,
    settings,
    draw_detection,
    draw_overlay,
    get_model_manager,
)

logger = logging.getLogger(__name__)


def validate_file_path(path: str, description: str = "File") -> None:
    """Ensure a required file exists on disk."""
    if not os.path.exists(path):
        logger.error(f"{description} not found at: {path}")
        print(f"[Error] {description} not found at: {path}")
        sys.exit(1)


def load_image(path: str) -> np.ndarray:
    """Load an image from disk using OpenCV with error handling."""
    validate_file_path(path, description="Image")
    img = cv2.imread(path)
    if img is None:
        logger.error(f"Failed to decode image: {path}")
        print(f"[Error] Failed to read image using OpenCV: {path}")
        sys.exit(1)
    return img


def resolve_model_paths(args: argparse.Namespace) -> Tuple[str, str]:
    """Resolve and ensure detection and recognition model paths using ModelManager."""
    mgr = get_model_manager()
    if args.det_model is not None:
        det_id = args.det_model
    else:
        det_id = f"face_detection/yolo-facev2{args.model_type}.onnx"

    rec_id = args.rec_model

    det_path = mgr.ensure_model(det_id, category="face_detection")
    rec_path = mgr.ensure_model(rec_id, category="face_recognition")

    return det_path, rec_path


def handle_list_models() -> None:
    """Print available detection and recognition models from Hugging Face Hub."""
    mgr = get_model_manager()
    print(f"\n[ModelManager] Fetching model catalog from Hugging Face ({mgr.repo_id})...")
    data = mgr.list_available_models(refresh_remote=True)
    status_str = "Online (Live Hub)" if data["online"] else "Offline (Local Cache)"
    print(f"Status: {status_str} | Repo: https://huggingface.co/{mgr.repo_id}\n")

    print("=" * 75)
    print(" FACE DETECTION MODELS")
    print("=" * 75)
    for m in data["face_detection"]:
        dl_status = f"[Downloaded - {m['size_mb']} MB]" if m["downloaded"] else "[Not Downloaded]"
        default_tag = " (DEFAULT)" if m["is_default"] else ""
        print(f" * {m['id']}{default_tag}")
        print(f"   Name: {m['name']} | Status: {dl_status}")
        if m["config"].get("description"):
            print(f"   Description: {m['config']['description']}")
        print()

    print("=" * 75)
    print(" FACE RECOGNITION MODELS")
    print("=" * 75)
    for m in data["face_recognition"]:
        dl_status = f"[Downloaded - {m['size_mb']} MB]" if m["downloaded"] else "[Not Downloaded]"
        default_tag = " (DEFAULT)" if m["is_default"] else ""
        print(f" * {m['id']}{default_tag}")
        print(f"   Name: {m['name']} | Status: {dl_status}")
        if m["config"].get("description"):
            print(f"   Description: {m['config']['description']}")
        print()


def handle_download_model(model_id: str) -> None:
    """Download a specific model explicitly via CLI."""
    mgr = get_model_manager()
    print(f"[ModelManager] Downloading model: '{model_id}'...")
    path = mgr.ensure_model(model_id, force_download=True)
    print(f"[ModelManager] Successfully downloaded to:\n  {path}\n")


# ============================================================================
# Action Handlers
# ============================================================================
def handle_compare(pipeline: FacePipeline, args: argparse.Namespace) -> None:
    """Compare two face images and determine if they belong to the same person."""
    img1_path, img2_path = args.compare
    img1 = load_image(img1_path)
    img2 = load_image(img2_path)

    print(f"\n[Compare] Comparing '{img1_path}' and '{img2_path}'...")
    res = pipeline.compare_faces(
        img1,
        img2,
        similarity_threshold=args.sim_thres,
        conf_threshold=args.conf_thres,
        iou_threshold=args.iou_thres,
    )

    if "error" in res:
        print(f"[Result] Comparison error: {res['error']}")
        return

    sim = res["similarity"]
    dist = res["euclidean_distance"]
    match_status = "MATCH (Same Person)" if res["match"] else "NO MATCH (Different Person)"

    print("\n" + "=" * 50)
    print(f" Cosine Similarity   : {sim:.4f} (Threshold: {res['threshold']})")
    print(f" Euclidean Distance  : {dist:.4f}")
    print(f" Status              : {match_status}")
    print("=" * 50 + "\n")

    if args.save_aligned:
        aligned_dir = os.path.join(args.output_dir, "aligned_faces")
        os.makedirs(aligned_dir, exist_ok=True)
        p1 = os.path.join(aligned_dir, "face1_aligned.jpg")
        p2 = os.path.join(aligned_dir, "face2_aligned.jpg")
        cv2.imwrite(p1, res["face1_aligned"])
        cv2.imwrite(p2, res["face2_aligned"])
        print(f"[Saved] Aligned faces saved to:\n  {p1}\n  {p2}")


def handle_detect(pipeline: FacePipeline, args: argparse.Namespace) -> None:
    """Detect all faces in a single image, align, and extract embeddings."""
    img_path = args.detect
    img = load_image(img_path)

    print(f"\n[Detect] Processing image: {img_path}")
    faces = pipeline.process_image(
        img,
        conf_threshold=args.conf_thres,
        iou_threshold=args.iou_thres
    )
    print(f"[Detect] Detected {len(faces)} face(s).")

    viz_img = img.copy()
    aligned_dir = os.path.join(args.output_dir, "aligned_faces")

    for idx, face in enumerate(faces):
        bbox = face["bbox"]
        conf = face["confidence"]
        emb = face["embedding"]

        print(f"  Face #{idx + 1}: BBox={bbox}, Conf={conf:.3f}, Embedding norm={np.linalg.norm(emb):.2f}")

        draw_detection(viz_img, bbox, label=f"Face {conf:.2f}")

        if args.save_aligned:
            os.makedirs(aligned_dir, exist_ok=True)
            crop_path = os.path.join(aligned_dir, f"detected_face_{idx + 1}.jpg")
            cv2.imwrite(crop_path, face["aligned_face"])
            print(f"    -> Saved aligned face to: {crop_path}")

    if args.save_viz:
        os.makedirs(args.output_dir, exist_ok=True)
        out_viz_path = os.path.join(args.output_dir, "detection_result.jpg")
        cv2.imwrite(out_viz_path, viz_img)
        print(f"[Saved] Annotated detection visualization saved to: {out_viz_path}")


def handle_webcam(pipeline: FacePipeline, args: argparse.Namespace) -> None:
    """Run real-time face detection on live webcam feed."""
    print("\n[Webcam] Initializing default webcam (device 0)...")
    cap = cv2.VideoCapture(0)
    if not cap.isOpened():
        print("[Error] Failed to open webcam. Ensure camera is connected and accessible.")
        return

    print("[Webcam] Stream started. Press 'q' to exit.\n")
    prev_time = cv2.getTickCount()

    try:
        while True:
            ret, frame = cap.read()
            if not ret:
                print("[Warn] Dropped webcam frame.")
                break

            faces = pipeline.process_image(
                frame,
                conf_threshold=args.conf_thres,
                iou_threshold=args.iou_thres
            )

            for face in faces:
                draw_detection(frame, face["bbox"], label=f"Conf: {face['confidence']:.2f}")

            # Calculate FPS
            curr_time = cv2.getTickCount()
            time_diff = (curr_time - prev_time) / cv2.getTickFrequency()
            fps = 1.0 / max(time_diff, 1e-5)
            prev_time = curr_time

            draw_overlay(frame, f"FPS: {fps:.1f} | Faces: {len(faces)}")
            cv2.imshow("Face Detection & Alignment - YOLO-FaceV2", frame)

            if cv2.waitKey(1) & 0xFF == ord('q'):
                break
    finally:
        cap.release()
        cv2.destroyAllWindows()
        print("\n[Webcam] Webcam session ended.")


def handle_video(pipeline: FacePipeline, args: argparse.Namespace) -> None:
    """Process an offline video file and optionally save the annotated result."""
    video_path = args.video
    validate_file_path(video_path, description="Video")

    cap = cv2.VideoCapture(video_path)
    if not cap.isOpened():
        print(f"[Error] Failed to open video file: {video_path}")
        return

    width = int(cap.get(cv2.CAP_PROP_FRAME_WIDTH))
    height = int(cap.get(cv2.CAP_PROP_FRAME_HEIGHT))
    fps = cap.get(cv2.CAP_PROP_FPS) or 30.0
    total_frames = int(cap.get(cv2.CAP_PROP_FRAME_COUNT))

    out_writer = None
    if args.save_viz:
        os.makedirs(args.output_dir, exist_ok=True)
        out_video_path = os.path.join(args.output_dir, "processed_video.mp4")
        fourcc = cv2.VideoWriter_fourcc(*"mp4v")
        out_writer = cv2.VideoWriter(out_video_path, fourcc, fps, (width, height))
        print(f"[Video] Output annotated video will be saved to: {out_video_path}")

    print(f"\n[Video] Processing '{video_path}' ({total_frames} frames)...")
    frame_idx = 0

    try:
        while True:
            ret, frame = cap.read()
            if not ret:
                break

            faces = pipeline.process_image(
                frame,
                conf_threshold=args.conf_thres,
                iou_threshold=args.iou_thres
            )

            for face in faces:
                draw_detection(frame, face["bbox"], label=f"Face {face['confidence']:.2f}")

            if out_writer:
                out_writer.write(frame)

            frame_idx += 1
            if frame_idx % 30 == 0 or frame_idx == total_frames:
                print(f"  Processed {frame_idx}/{total_frames} frames ({(frame_idx / max(total_frames, 1)) * 100:.1f}%)")
    finally:
        cap.release()
        if out_writer:
            out_writer.release()
        print(f"[Video] Finished processing {frame_idx} frames.\n")


# ============================================================================
# Argument Parser & Entry Point
# ============================================================================
def parse_arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Unified YOLO-FaceV2 Face Detection & ArcFace Recognition CLI.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  0. List all available models from Hugging Face:
     python main.py --list-models

  1. Compare two face images:
     python main.py --compare person1.jpg person2.jpg --model-type s

  2. Detect & recognize faces in a single image:
     python main.py --detect sample.jpg --save-viz --save-aligned

  3. Process a video file:
     python main.py --video sample.mp4 --save-viz

  4. Run live webcam face recognition:
     python main.py --webcam --model-type s
        """
    )
    parser.add_argument(
        "--list-models",
        action="store_true",
        help="Query and display all detection and recognition models available on Hugging Face Hub."
    )
    parser.add_argument(
        "--download-model",
        type=str,
        metavar="MODEL_ID",
        help="Download a specific model from Hugging Face Hub (e.g. face_detection/yolo-facev2m.onnx)."
    )
    parser.add_argument(
        "--model-type",
        choices=["s", "m"],
        default="s",
        help="YOLO-FaceV2 model size: 's' (fast) or 'm' (higher accuracy). Default: s"
    )
    parser.add_argument(
        "--det-model",
        type=str,
        default=None,
        help="Custom detection model ID or file path. If omitted, uses default YOLO-FaceV2 model."
    )
    parser.add_argument(
        "--rec-model",
        type=str,
        default=settings.rec_model_path,
        help=f"Recognition model ID or file path. Default: {settings.rec_model_path}"
    )
    parser.add_argument(
        "--compare",
        nargs=2,
        metavar=("IMG1", "IMG2"),
        help="Compare two face images and check similarity."
    )
    parser.add_argument(
        "--detect",
        type=str,
        help="Path to an image to detect faces, perform alignment, and extract embeddings."
    )
    parser.add_argument(
        "--webcam",
        action="store_true",
        help="Run real-time detection & alignment from default webcam."
    )
    parser.add_argument(
        "--video",
        type=str,
        help="Path to video file for offline face detection."
    )
    parser.add_argument(
        "--conf-thres",
        type=float,
        default=settings.conf_threshold,
        help=f"Confidence threshold for YOLO face detector (default: {settings.conf_threshold})."
    )
    parser.add_argument(
        "--iou-thres",
        type=float,
        default=settings.iou_threshold,
        help=f"NMS IoU threshold for YOLO face detector (default: {settings.iou_threshold})."
    )
    parser.add_argument(
        "--sim-thres",
        type=float,
        default=settings.sim_threshold,
        help=f"Cosine similarity threshold for ArcFace matching (default: {settings.sim_threshold})."
    )
    parser.add_argument(
        "--save-aligned",
        action="store_true",
        help="Save aligned face crops to disk."
    )
    parser.add_argument(
        "--save-viz",
        action="store_true",
        help="Save visualization image/video with bounding boxes to disk."
    )
    parser.add_argument(
        "--output-dir",
        type=str,
        default="output",
        help="Directory to save aligned faces and visualizations (default: output)."
    )
    return parser.parse_args()


def main() -> None:
    logging.basicConfig(
        level=getattr(logging, settings.log_level, logging.INFO),
        format="%(asctime)s [%(levelname)s] %(name)s: %(message)s"
    )
    args = parse_arguments()

    if args.list_models:
        handle_list_models()
        return

    if args.download_model:
        handle_download_model(args.download_model)
        return

    det_path, rec_path = resolve_model_paths(args)

    # Initialize Pipeline
    pipeline = FacePipeline(
        det_model_path=det_path,
        rec_model_path=rec_path,
        conf_threshold=args.conf_thres,
        iou_threshold=args.iou_thres
    )

    os.makedirs(args.output_dir, exist_ok=True)

    if args.compare:
        handle_compare(pipeline, args)
    elif args.detect:
        handle_detect(pipeline, args)
    elif args.webcam:
        handle_webcam(pipeline, args)
    elif args.video:
        handle_video(pipeline, args)
    else:
        print("[Info] No action specified. Use --help to view available options.")
        print("Example quick test:")
        print("  python main.py --list-models")
        print("  python main.py --help")


if __name__ == "__main__":
    main()
