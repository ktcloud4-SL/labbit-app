"""Live reruns must create a new directory outside this immutable evidence package."""
import os,pathlib,sys
sys.dont_write_bytecode=True

def reserve_output(filename):
    directory=pathlib.Path(os.environ['C3_EVIDENCE_OUTPUT_DIR']).expanduser().resolve()
    package=pathlib.Path(__file__).resolve().parent
    if directory==package or package in directory.parents:
        raise ValueError('choose a new output directory outside the committed evidence')
    if pathlib.Path(filename).name!=filename:
        raise ValueError('output must be a basename')
    directory.mkdir(parents=True,exist_ok=False)
    output=directory/filename
    with output.open('xb'):
        pass
    return output
