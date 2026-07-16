import json

from habitus.online.llm import FakeLLM, LLMResponse
from habitus.online.object_qa import UNKNOWN_TEXT, answer_object
from habitus.online.schema import ObjectAskRequest


def request():
    return ObjectAskRequest(
        question="Какой шум?",
        passport={"lifestyle_analysis": {"blocks": [{"data": {"db": 35}}]}},
    )


def test_answer_keeps_only_existing_evidence_paths():
    payload = {"sentences": [{
        "text": "Под окном 35 дБ.",
        "evidence_paths": ["$.lifestyle_analysis.blocks.0.data.db"],
    }]}
    result = answer_object(request(), FakeLLM([
        LLMResponse(content=None, tool_arguments=json.dumps(payload))]))
    assert result.sentences[0].text == "Под окном 35 дБ."


def test_answer_replaces_invalid_evidence_with_unknown():
    payload = {"sentences": [{
        "text": "Рядом аэропорт.", "evidence_paths": ["$.invented.airport"],
    }]}
    result = answer_object(request(), FakeLLM([
        LLMResponse(content=None, tool_arguments=json.dumps(payload))]))
    assert result.sentences[0].unknown is True
    assert result.sentences[0].text == UNKNOWN_TEXT
