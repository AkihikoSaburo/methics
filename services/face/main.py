"""
Face Detection (YOLO-FaceV2) & Face Recognition (ArcFace) — API Launcher
=========================================================================
Starts the FastAPI server for face detection and recognition.
All functionality is exposed via REST API endpoints.

Run:
    python main.py
    python main.py --host 0.0.0.0 --port 8000
    python main.py --list-models
    python main.py --download-model face_detection/yolo-facev2m.onnx
"""

import argparse
import logging
import sys

from src import settings, get_model_manager


logger = logging.getLogger(__name__)


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


def parse_arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Face Recognition API Server — YOLO-FaceV2 & ArcFace.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""\
Examples:
  0. Start the API server (default port 8000):
     python main.py

  1. Start on custom host/port:
     python main.py --host 0.0.0.0 --port 9000

  2. List all available models from Hugging Face:
     python main.py --list-models

  3. Download a model manually:
     python main.py --download-model face_detection/yolo-facev2m.onnx

  4. Start with auto-reload for development:
     python main.py --reload
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
        "--host",
        type=str,
        default="0.0.0.0",
        help="Host address to bind the API server (default: 0.0.0.0)."
    )
    parser.add_argument(
        "--port",
        type=int,
        default=8000,
        help="Port number for the API server (default: 8000)."
    )
    parser.add_argument(
        "--reload",
        action="store_true",
        help="Enable auto-reload for development."
    )
    parser.add_argument(
        "--workers",
        type=int,
        default=1,
        help="Number of uvicorn worker processes (default: 1)."
    )
    return parser.parse_args()


def main() -> None:
    logging.basicConfig(
        level=getattr(logging, settings.log_level, logging.INFO),
        format="%(asctime)s [%(levelname)s] %(name)s: %(message)s"
    )
    args = parse_arguments()

    # Handle model management commands (no server needed)
    if args.list_models:
        handle_list_models()
        return

    if args.download_model:
        handle_download_model(args.download_model)
        return

    # Start the FastAPI server
    try:
        import uvicorn
    except ImportError:
        print("[Error] uvicorn is not installed. Install it with: pip install uvicorn")
        sys.exit(1)

    print(f"\n{'=' * 60}")
    print(f" Face Recognition API Server")
    print(f" Host: {args.host} | Port: {args.port}")
    print(f" Swagger UI: http://{args.host}:{args.port}/docs")
    print(f" ReDoc: http://{args.host}:{args.port}/redoc")
    print(f"{'=' * 60}\n")

    uvicorn.run(
        "api:app",
        host=args.host,
        port=args.port,
        reload=args.reload,
        workers=args.workers,
    )


if __name__ == "__main__":
    main()
