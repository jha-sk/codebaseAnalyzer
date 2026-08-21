import os

import subprocess


def run_it():
    subprocess.run("ls -la", shell=True)


def get_count() -> int:
    return "not a number"
