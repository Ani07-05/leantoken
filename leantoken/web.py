"""Tavily web search, shaped to cost few tokens: short answer, 3 results, clipped snippets."""

import os

import httpx

from .tools import clip

URL = "https://api.tavily.com/search"
SNIPPET = 300


def search(query: str, max_results: int = 3) -> str:
    key = os.environ.get("TAVILY_API_KEY")
    if not key:
        return "error: TAVILY_API_KEY is not set"
    r = httpx.post(
        URL,
        headers={"Authorization": f"Bearer {key}"},
        json={"query": query, "max_results": max(1, min(int(max_results), 5)), "include_answer": "basic"},
        timeout=30,
    )
    if r.status_code >= 400:
        return f"error: tavily {r.status_code}: {r.text[:200]}"
    data = r.json()
    lines = [data["answer"]] if data.get("answer") else []
    lines += [f"- {x['title']} <{x['url']}>\n  {x.get('content', '')[:SNIPPET]}" for x in data.get("results", [])]
    return clip("\n".join(lines) or "no results")
