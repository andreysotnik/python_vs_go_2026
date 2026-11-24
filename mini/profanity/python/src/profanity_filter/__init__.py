"""Regex-based profanity filter for Russian mat and basic English swear words."""

from profanity_filter.core import Match, censor, check, classify, find_matches

__all__ = ["Match", "censor", "check", "classify", "find_matches"]
