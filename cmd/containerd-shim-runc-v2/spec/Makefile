WORKERS := 4

TLA := docker run --rm -it --workdir /mnt -v ${PWD}:/mnt talex5/tla

.PHONY: all check pdfs

all: check pdfs

# Run the TLC model checker
check:
	${TLA} tlc -workers ${WORKERS} Shim.tla -config Shim.cfg

# Generate a PDF file from a .tla file
%.pdf: %.tla
	[ -d .metadir ] || mkdir .metadir
	${TLA} java tla2tex.TLA -shade -latexCommand pdflatex -latexOutputExt pdf -metadir .metadir $<

pdfs: Shim.pdf Environment.pdf Types.pdf 
