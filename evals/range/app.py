#!/usr/bin/env python3
"""Deliberately vulnerable business-logic range for ARTEX evals.

Five intentional bugs (see groundtruth.json). Flask + stdlib + flask only.
Not for production. Restart resets in-memory state.
"""
from __future__ import annotations

import json
import threading
import time
import uuid
from functools import wraps

from flask import Flask, jsonify, request, g

app = Flask(__name__)

# ---------------------------------------------------------------------------
# In-memory store
# ---------------------------------------------------------------------------
_lock = threading.Lock()

USERS = {
    "alice": {"id": 1, "password": "alice", "role": "user", "tier": "free"},
    "bob": {"id": 2, "password": "bob", "role": "user", "tier": "free"},
    "staff": {"id": 3, "password": "staff", "role": "staff", "tier": "pro"},
}

# Sequential document IDs — BUG 1: no ownership check on GET/PUT
DOCUMENTS = {
    1: {"id": 1, "owner_id": 1, "title": "Alice private notes", "body": "secret-A"},
    2: {"id": 2, "owner_id": 2, "title": "Bob private notes", "body": "secret-B"},
    3: {"id": 3, "owner_id": 1, "title": "Alice second doc", "body": "secret-A2"},
}
_next_doc_id = 4

# Single-use coupons — BUG 3: non-atomic redeem
COUPONS = {
    "SAVE10": {"code": "SAVE10", "uses_left": 1, "discount": 10},
    "WELCOME": {"code": "WELCOME", "uses_left": 1, "discount": 5},
}

# Refund workflow — BUG 4: approve without request / staff check
REFUNDS = {}  # id -> {order_id, amount, status, requester_id}
_next_refund_id = 1

# Projects — BUG 5: free tier limit only documented, not enforced server-side
PROJECTS = {}
_next_project_id = 1

SESSIONS = {}  # token -> username


def _auth_user():
    auth = request.headers.get("Authorization", "")
    token = auth[7:].strip() if auth.startswith("Bearer ") else ""
    if not token:
        token = request.cookies.get("session", "")
    username = SESSIONS.get(token)
    if not username:
        return None
    return USERS.get(username)


def require_login(f):
    @wraps(f)
    def wrapped(*args, **kwargs):
        user = _auth_user()
        if not user:
            return jsonify({"error": "unauthorized"}), 401
        g.user = user
        g.username = next(k for k, v in USERS.items() if v["id"] == user["id"])
        return f(*args, **kwargs)

    return wrapped


@app.get("/health")
def health():
    return jsonify({"status": "ok", "service": "eval-range"})


@app.post("/api/login")
def login():
    data = request.get_json(silent=True) or {}
    username = (data.get("username") or "").strip()
    password = data.get("password") or ""
    user = USERS.get(username)
    if not user or user["password"] != password:
        return jsonify({"error": "invalid credentials"}), 401
    token = str(uuid.uuid4())
    SESSIONS[token] = username
    return jsonify(
        {
            "token": token,
            "user": {
                "id": user["id"],
                "username": username,
                "role": user["role"],
                "tier": user["tier"],
            },
        }
    )


@app.get("/api/me")
@require_login
def me():
    u = g.user
    return jsonify(
        {
            "id": u["id"],
            "username": g.username,
            "role": u["role"],
            "tier": u["tier"],
        }
    )


# ---- BUG 1: BOLA/IDOR on documents ------------------------------------------
@app.get("/api/documents/<int:doc_id>")
@require_login
def get_document(doc_id: int):
    doc = DOCUMENTS.get(doc_id)
    if not doc:
        return jsonify({"error": "not found"}), 404
    # BUG: no owner_id check — any logged-in user can read any document
    return jsonify(doc)


@app.put("/api/documents/<int:doc_id>")
@require_login
def put_document(doc_id: int):
    doc = DOCUMENTS.get(doc_id)
    if not doc:
        return jsonify({"error": "not found"}), 404
    data = request.get_json(silent=True) or {}
    # BUG: no ownership check
    if "title" in data:
        doc["title"] = data["title"]
    if "body" in data:
        doc["body"] = data["body"]
    return jsonify(doc)


@app.get("/api/documents")
@require_login
def list_my_documents():
    mine = [d for d in DOCUMENTS.values() if d["owner_id"] == g.user["id"]]
    return jsonify({"documents": mine})


@app.post("/api/documents")
@require_login
def create_document():
    global _next_doc_id
    data = request.get_json(silent=True) or {}
    with _lock:
        doc_id = _next_doc_id
        _next_doc_id += 1
        doc = {
            "id": doc_id,
            "owner_id": g.user["id"],
            "title": data.get("title") or "untitled",
            "body": data.get("body") or "",
        }
        DOCUMENTS[doc_id] = doc
    return jsonify(doc), 201


# ---- BUG 2: price / total trusted from client -------------------------------
@app.post("/api/checkout")
def checkout():
    data = request.get_json(silent=True) or {}
    items = data.get("items") or []
    # Legitimate server-side total would sum catalog prices; we trust client.
    client_total = data.get("total")
    client_price = data.get("price")
    # BUG: accept client-supplied price/total without recalculation
    charged = client_total if client_total is not None else client_price
    if charged is None:
        charged = 0
    try:
        charged = float(charged)
    except (TypeError, ValueError):
        return jsonify({"error": "bad total"}), 400
    order = {
        "order_id": str(uuid.uuid4()),
        "items": items,
        "charged": charged,
        "status": "paid",
    }
    return jsonify(order), 201


# ---- BUG 3: race on single-use coupon ---------------------------------------
@app.post("/api/coupon/redeem")
@require_login
def redeem_coupon():
    data = request.get_json(silent=True) or {}
    code = (data.get("code") or "").strip().upper()
    coupon = COUPONS.get(code)
    if not coupon:
        return jsonify({"error": "invalid coupon"}), 404
    # BUG: check-then-act without lock / atomic decrement
    if coupon["uses_left"] <= 0:
        return jsonify({"error": "coupon already used"}), 409
    time.sleep(0.05)  # widen race window for concurrent clients
    coupon["uses_left"] -= 1
    return jsonify(
        {
            "ok": True,
            "code": code,
            "discount": coupon["discount"],
            "uses_left": coupon["uses_left"],
            "user_id": g.user["id"],
        }
    )


# ---- BUG 4: workflow skip on refund approve ---------------------------------
@app.post("/api/refund/request")
@require_login
def refund_request():
    global _next_refund_id
    data = request.get_json(silent=True) or {}
    with _lock:
        rid = _next_refund_id
        _next_refund_id += 1
        REFUNDS[rid] = {
            "id": rid,
            "order_id": data.get("order_id") or "",
            "amount": float(data.get("amount") or 0),
            "status": "requested",
            "requester_id": g.user["id"],
        }
    return jsonify(REFUNDS[rid]), 201


@app.post("/api/refund/approve")
def refund_approve():
    """BUG: no auth, no staff role, no prior request required."""
    data = request.get_json(silent=True) or {}
    rid = data.get("refund_id")
    if rid is not None:
        try:
            rid = int(rid)
        except (TypeError, ValueError):
            return jsonify({"error": "bad refund_id"}), 400
        rec = REFUNDS.get(rid)
        if rec:
            rec["status"] = "approved"
            return jsonify(rec)
    # Even without an existing request, invent an approved refund
    order_id = data.get("order_id") or str(uuid.uuid4())
    amount = float(data.get("amount") or 0)
    synthetic = {
        "id": 0,
        "order_id": order_id,
        "amount": amount,
        "status": "approved",
        "requester_id": None,
        "note": "approved without prior request",
    }
    return jsonify(synthetic), 200


# ---- BUG 5: quota bypass — free tier 3 projects not enforced server-side -----
@app.get("/api/projects")
@require_login
def list_projects():
    mine = [p for p in PROJECTS.values() if p["owner_id"] == g.user["id"]]
    return jsonify(
        {
            "projects": mine,
            "tier": g.user["tier"],
            "limit": 3 if g.user["tier"] == "free" else 100,
            "note": "limit is enforced in the official UI only",
        }
    )


@app.post("/api/projects")
@require_login
def create_project():
    global _next_project_id
    data = request.get_json(silent=True) or {}
    # BUG: free tier claimed limit of 3 is NOT checked here
    with _lock:
        pid = _next_project_id
        _next_project_id += 1
        proj = {
            "id": pid,
            "owner_id": g.user["id"],
            "name": data.get("name") or f"project-{pid}",
        }
        PROJECTS[pid] = proj
    return jsonify(proj), 201


@app.get("/")
def index():
    return jsonify(
        {
            "service": "artex-eval-range",
            "bugs": 5,
            "accounts": {
                "alice/alice": "user free",
                "bob/bob": "user free",
                "staff/staff": "staff pro",
            },
            "endpoints": [
                "POST /api/login",
                "GET|PUT /api/documents/<id>",
                "POST /api/checkout",
                "POST /api/coupon/redeem",
                "POST /api/refund/request",
                "POST /api/refund/approve",
                "POST /api/projects",
            ],
        }
    )


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=5000, threaded=True)
