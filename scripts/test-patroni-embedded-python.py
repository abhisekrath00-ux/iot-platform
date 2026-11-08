#!/usr/bin/env python3
"""Compile literal Python -c programs in the Patroni shell fixture, without running them.

This is deliberately a syntax gate, not a shell parser or daemon/runtime test.
"""
from pathlib import Path
import shlex
import unittest

FIXTURE = Path(__file__).resolve().with_name('test-patroni-image-bootstrap.sh')
PYTHON = '/opt/patroni/bin/python'
EXPECTED_PROGRAMS = 4


def programs(source):
    # shlex preserves the contents of quoted multiline arguments. It does not
    # expand shell variables, run command substitutions or execute the script.
    words = list(shlex.shlex(source, posix=True, punctuation_chars=True))
    result = []
    for index, word in enumerate(words):
        if word == PYTHON:
            if words[index + 1:index + 2] != ['-c'] or index + 2 >= len(words):
                raise ValueError('fixture Python invocation is not a literal -c program')
            program = words[index + 2]
            if not program or program.startswith('$'):
                raise ValueError('fixture Python program must be literal')
            result.append(program)
    if len(result) != EXPECTED_PROGRAMS:
        raise ValueError(f'expected {EXPECTED_PROGRAMS} Python programs, found {len(result)}; review gate when changing invocations')
    return result


def validate(source):
    found = programs(source)
    for number, program in enumerate(found, 1):
        compile(program, f'{FIXTURE.name}:python-c-{number}', 'exec')
    return found


class EmbeddedPythonTests(unittest.TestCase):
    def setUp(self):
        self.source = FIXTURE.read_text(encoding='utf-8')

    def test_actual_fixture_programs_compile(self):
        self.assertEqual(len(validate(self.source)), EXPECTED_PROGRAMS)

    def test_ci1513_indented_try_is_rejected(self):
        original = 'import urllib.request,urllib.error\ntry:'
        self.assertEqual(self.source.count(original), 1)
        broken = self.source.replace(original, 'import urllib.request,urllib.error\n  try:', 1)
        with self.assertRaises(IndentationError):
            validate(broken)

    def test_syntax_error_is_rejected(self):
        original = 'import json,urllib.request;'
        self.assertIn(original, self.source)
        with self.assertRaises(SyntaxError):
            validate(self.source.replace(original, 'import (json;', 1))

    def test_missing_invocation_requires_review(self):
        with self.assertRaises(ValueError):
            validate(self.source.replace(PYTHON, '/fixture-not-python', 1))

    def test_program_is_not_executed(self):
        source = '\n'.join(f"{PYTHON} -c 'raise RuntimeError()'" for _ in range(EXPECTED_PROGRAMS))
        self.assertEqual(len(validate(source)), EXPECTED_PROGRAMS)


if __name__ == '__main__':
    unittest.main(verbosity=2)
