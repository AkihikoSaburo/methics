"""
Model Manager for Hugging Face Hub Model Integration
=====================================================
Manages downloading, caching, discovery, and configuration of ONNX models
from Hugging Face (default repo: AkihikoSaburo/methics).
"""

import json
import logging
import os
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

logger = logging.getLogger(__name__)

DEFAULT_CONFIG_PATH = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
    "models_config.json"
)
DEFAULT_MODELS_DIR = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
    "models"
)
DEFAULT_HF_REPO = "AkihikoSaburo/methics"


class ModelManager:
    """
    Orchestrates ONNX model assets:
    - Queries available models from Hugging Face repository in real-time
    - Resolves model identifiers and local cache paths
    - Automatically downloads requested models if absent locally
    - Associates models with configuration parameters (input size, thresholds, etc.)
    """

    def __init__(
        self,
        repo_id: Optional[str] = None,
        config_path: Optional[str] = None,
        models_dir: Optional[str] = None,
        token: Optional[str] = None
    ):
        self.config_path = config_path or os.getenv("MODELS_CONFIG_FILE", DEFAULT_CONFIG_PATH)
        self.models_dir = Path(models_dir or os.getenv("MODELS_DIR", DEFAULT_MODELS_DIR))
        self.token = token or os.getenv("HF_TOKEN")
        self.config_data = self._load_config()

        # HF Repo ID can be overridden via constructor, env, or config file
        self.repo_id = (
            repo_id
            or os.getenv("HF_REPO_ID")
            or self.config_data.get("hf_repo_id", DEFAULT_HF_REPO)
        )

        self.models_dir.mkdir(parents=True, exist_ok=True)
        (self.models_dir / "face_detection").mkdir(parents=True, exist_ok=True)
        (self.models_dir / "face_recognition").mkdir(parents=True, exist_ok=True)

    def _load_config(self) -> Dict[str, Any]:
        """Load models_config.json from disk with fallback to safe defaults."""
        if os.path.exists(self.config_path):
            try:
                with open(self.config_path, "r", encoding="utf-8") as f:
                    return json.load(f)
            except Exception as e:
                logger.warning(f"Failed to parse {self.config_path}: {e}. Using fallback defaults.")

        return {
            "version": "1.0",
            "hf_repo_id": DEFAULT_HF_REPO,
            "defaults": {
                "face_detection": "face_detection/yolo-facev2s.onnx",
                "face_recognition": "face_recognition/arcface_r50_fp16.onnx"
            },
            "models": {}
        }

    def get_defaults(self) -> Dict[str, str]:
        """Return the default model relative paths for detection and recognition."""
        return self.config_data.get("defaults", {
            "face_detection": "face_detection/yolo-facev2s.onnx",
            "face_recognition": "face_recognition/arcface_r50_fp16.onnx"
        })

    def get_default_model(self, category: str) -> str:
        """Return default model path for a category ('face_detection' or 'face_recognition')."""
        defaults = self.get_defaults()
        if category in defaults:
            return defaults[category]
        raise ValueError(f"Unknown model category: '{category}'. Expected 'face_detection' or 'face_recognition'.")

    def list_available_models(self, refresh_remote: bool = True) -> Dict[str, Any]:
        """
        List all models available on the Hugging Face repository and locally.

        Returns:
            Dict structured as:
            {
                "repo_id": "...",
                "defaults": { ... },
                "face_detection": [ { ...model info... } ],
                "face_recognition": [ { ...model info... } ]
            }
        """
        remote_files: List[str] = []
        is_online = False

        if refresh_remote:
            try:
                from huggingface_hub import HfApi
                api = HfApi(token=self.token)
                all_files = api.list_repo_files(repo_id=self.repo_id)
                remote_files = [f for f in all_files if f.endswith(".onnx")]
                is_online = True
            except Exception as e:
                logger.warning(f"Could not connect to Hugging Face Hub ({self.repo_id}): {e}. Falling back to local cache.")

        # If offline or listing failed, scan local models directory and config
        known_models = set(remote_files)
        for cat in ("face_detection", "face_recognition"):
            cat_dir = self.models_dir / cat
            if cat_dir.exists():
                for f in cat_dir.glob("*.onnx"):
                    known_models.add(f"{cat}/{f.name}")

        for configured_rel_path in self.config_data.get("models", {}):
            known_models.add(configured_rel_path)

        result: Dict[str, Any] = {
            "repo_id": self.repo_id,
            "defaults": self.get_defaults(),
            "online": is_online,
            "face_detection": [],
            "face_recognition": []
        }

        configured_models = self.config_data.get("models", {})

        for rel_path in sorted(list(known_models)):
            parts = rel_path.split("/")
            category = parts[0] if len(parts) > 1 else ("face_detection" if "yolo" in rel_path.lower() else "face_recognition")
            filename = parts[-1]

            local_file = self.models_dir / rel_path
            is_downloaded = local_file.is_file()
            file_size_mb = round(local_file.stat().st_size / (1024 * 1024), 2) if is_downloaded else None

            # Base info from config or smart default
            cfg = configured_models.get(rel_path, {})
            model_info = {
                "id": rel_path,
                "filename": filename,
                "category": category,
                "name": cfg.get("name", filename.replace(".onnx", "").replace("-", " ").title()),
                "architecture": cfg.get("architecture", "yolo-facev2" if category == "face_detection" else "insightface"),
                "downloaded": is_downloaded,
                "size_mb": file_size_mb,
                "is_default": (self.get_defaults().get(category) == rel_path),
                "config": cfg
            }

            if category in result:
                result[category].append(model_info)

        return result

    def resolve_model_id(self, model_identifier: str, category: Optional[str] = None) -> str:
        """
        Normalize user inputs into standard repo relative path (e.g. 'face_detection/yolo-facev2s.onnx').

        Examples:
            'yolo-facev2s' -> 'face_detection/yolo-facev2s.onnx'
            'yolo-facev2s.onnx' -> 'face_detection/yolo-facev2s.onnx'
            'face_detection/yolo-facev2s.onnx' -> 'face_detection/yolo-facev2s.onnx'
        """
        # If absolute or existing path, leave as-is
        if os.path.isabs(model_identifier) or os.path.exists(model_identifier):
            return model_identifier

        # Ensure .onnx extension
        clean_id = model_identifier
        if not clean_id.endswith(".onnx"):
            clean_id = f"{clean_id}.onnx"

        # If already formatted with folder
        if "/" in clean_id:
            return clean_id

        # Infer category if not provided
        if not category:
            if "yolo" in clean_id.lower() or "det" in clean_id.lower():
                category = "face_detection"
            else:
                category = "face_recognition"

        return f"{category}/{clean_id}"

    def ensure_model(
        self,
        model_identifier: str,
        category: Optional[str] = None,
        force_download: bool = False
    ) -> str:
        """
        Ensure the model exists locally. If missing, download it from Hugging Face Hub.

        Args:
            model_identifier: Model name, relative path, or direct file path.
            category: Optional 'face_detection' or 'face_recognition'.
            force_download: If True, re-downloads even if file exists.

        Returns:
            Absolute path to the validated model file on disk.
        """
        # Case 1: Direct local file path that already exists
        if os.path.exists(model_identifier) and os.path.isfile(model_identifier):
            return os.path.abspath(model_identifier)

        # Case 2: Resolve to repo relative path (e.g. face_detection/yolo-facev2s.onnx)
        rel_path = self.resolve_model_id(model_identifier, category=category)
        target_path = self.models_dir / rel_path

        if target_path.exists() and not force_download:
            return str(target_path.resolve())

        # Target not found locally, download from Hugging Face
        logger.info(f"Model file not found at {target_path}. Downloading from Hugging Face: {self.repo_id} / {rel_path} ...")
        print(f"[ModelManager] Downloading '{rel_path}' from https://huggingface.co/{self.repo_id} ...")

        try:
            from huggingface_hub import hf_hub_download
            target_path.parent.mkdir(parents=True, exist_ok=True)

            downloaded_file = hf_hub_download(
                repo_id=self.repo_id,
                filename=rel_path,
                local_dir=str(self.models_dir),
                token=self.token,
                force_download=force_download
            )
            logger.info(f"Successfully downloaded model to: {downloaded_file}")
            print(f"[ModelManager] Download complete: {downloaded_file}")
            return str(Path(downloaded_file).resolve())
        except Exception as e:
            logger.error(f"Failed to download model '{rel_path}' from Hugging Face ({self.repo_id}): {e}")
            raise FileNotFoundError(
                f"Model '{rel_path}' could not be found locally or downloaded from "
                f"Hugging Face repository '{self.repo_id}'. Error: {e}"
            )

    def get_model_config(self, model_identifier: str, category: Optional[str] = None) -> Dict[str, Any]:
        """Retrieve model metadata/config dictionary."""
        rel_path = self.resolve_model_id(model_identifier, category=category)
        configured = self.config_data.get("models", {}).get(rel_path)
        if configured:
            return configured

        # Fallback profile
        is_det = "face_detection" in rel_path or "yolo" in rel_path.lower()
        if is_det:
            return {
                "name": Path(rel_path).stem.title(),
                "architecture": "yolo-facev2",
                "input_size": 640,
                "conf_threshold": 0.35,
                "iou_threshold": 0.45
            }
        else:
            return {
                "name": Path(rel_path).stem.title(),
                "architecture": "insightface",
                "input_size": [112, 112],
                "norm_type": "insightface",
                "embedding_dim": 512,
                "sim_threshold": 0.45
            }


# Global singleton instance
default_manager = ModelManager()


def get_model_manager() -> ModelManager:
    """Return the global ModelManager singleton."""
    return default_manager
