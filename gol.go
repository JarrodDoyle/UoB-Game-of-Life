package main

import (
	"strconv"
	"strings"
)

type wChans struct {
	input  chan uint8
	output chan uint8
}

func receiveRow(width int, val chan byte) []byte {
	row := make([]byte, width)
	for x := 0; x < width; x++ {
		row[x] = <-val
	}
	return row
}

func worker(p golParams, chans wChans) {
	sliceHeight := (p.imageHeight / p.threads) + 2
	workerSlice := make([][]byte, sliceHeight)
	for i := range workerSlice {
		workerSlice[i] = make([]byte, p.imageWidth)
	}

	for i := 0; i < p.turns; i++ {
		// Receive top row
		workerSlice[0] = receiveRow(p.imageWidth, chans.input)
		// Receive center section if first turn
		if i == 0 {
			for j := 1; j < sliceHeight-1; j++ {
				workerSlice[j] = receiveRow(p.imageWidth, chans.input)
			}
		}
		// Receive bottom row
		workerSlice[sliceHeight-1] = receiveRow(p.imageWidth, chans.input)

		// Create temporary slice
		newSlice := make([][]byte, sliceHeight)
		for i := range newSlice {
			newSlice[i] = make([]byte, p.imageWidth)
		}

		// Process center and update workerSlice
		for y := 1; y < sliceHeight-1; y++ {
			row := make([]byte, p.imageWidth)
			for x := 0; x < p.imageWidth; x++ {
				s := workerSlice
				w := p.imageWidth
				aliveNeighbours := (int(s[y-1][(x-1+w)%w]) + int(s[y-1][x]) + int(s[y-1][(x+1)%w]) + int(s[y][(x-1+w)%w]) +
					int(s[y][(x+1)%w]) + int(s[y+1][(x-1+w)%w]) + int(s[y+1][x]) + int(s[y+1][(x+1)%w])) / 255

				row[x] = workerSlice[y][x]
				if workerSlice[y][x] != 0 {
					if !(aliveNeighbours == 2 || aliveNeighbours == 3) {
						row[x] = 0x00
					}
				} else if aliveNeighbours == 3 {
					row[x] = 0xFF
				}
				chans.output <- row[x]
			}
			newSlice[y] = row
		}
		workerSlice = newSlice
	}
}

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p golParams, d distributorChans, alive chan []cell) {

	// Create the 2D slice to store the world.
	world := make([][]byte, p.imageHeight)
	for i := range world {
		world[i] = make([]byte, p.imageWidth)
	}

	// Request the io goroutine to read in the image with the given filename.
	d.io.command <- ioInput
	d.io.filename <- strings.Join([]string{strconv.Itoa(p.imageWidth), strconv.Itoa(p.imageHeight)}, "x")

	// The io goroutine sends the requested image byte by byte, in rows.
	for y := 0; y < p.imageHeight; y++ {
		for x := 0; x < p.imageWidth; x++ {
			val := <-d.io.inputVal
			if val != 0 {
				world[y][x] = val
			}
		}
	}

	// Create worker channels and initialise worker threads
	workerHeight := p.imageHeight / p.threads
	workerChannels := make([]wChans, p.threads)
	for i := 0; i < p.threads; i++ {
		var wChans wChans
		wChans.input = make(chan byte, p.imageWidth*(workerHeight+2))
		wChans.output = make(chan byte, p.imageWidth*workerHeight)
		workerChannels[i] = wChans
		go worker(p, workerChannels[i])
	}

	// Calculate the new state of Game of Life after the given number of turns.
	for turn := 0; turn < p.turns; turn++ {
		// send rows to workers
		for i := 0; i < p.threads; i++ {
			// Send top row
			y := ((i * workerHeight) - 1 + p.imageHeight) % p.imageHeight
			for x := 0; x < p.imageWidth; x++ {
				workerChannels[i].input <- world[y][x]
			}
			// Send center rows if turn 0
			if turn == 0 {
				for y := i * workerHeight; y < (i+1)*workerHeight; y++ {
					for x := 0; x < p.imageWidth; x++ {
						workerChannels[i].input <- world[y][x]
					}
				}
			}
			// Send bottom row
			y = ((i + 1) * workerHeight) % p.imageHeight
			for x := 0; x < p.imageWidth; x++ {
				workerChannels[i].input <- world[y][x]
			}
		}

		// Receive rows from workers
		for i := 0; i < workerHeight; i++ {
			for j := 0; j < p.threads; j++ {
				world[(j*workerHeight)+i] = receiveRow(p.imageWidth, workerChannels[j].output)
			}
		}
	}

	// Request the io goroutine to write in the image with the given filename.
	d.io.command <- ioOutput
	d.io.filename <- strings.Join([]string{strconv.Itoa(p.imageWidth), strconv.Itoa(p.imageHeight), strconv.Itoa(p.turns)}, "x")

	for y := 0; y < p.imageHeight; y++ {
		for x := 0; x < p.imageWidth; x++ {
			d.io.outputVal <- world[y][x]
		}
	}

	// Create an empty slice to store coordinates of cells that are still alive after p.turns are done.
	var finalAlive []cell
	// Go through the world and append the cells that are still alive.
	for y := 0; y < p.imageHeight; y++ {
		for x := 0; x < p.imageWidth; x++ {
			if world[y][x] != 0 {
				finalAlive = append(finalAlive, cell{x: x, y: y})
			}
		}
	}

	// Make sure that the Io has finished any output before exiting.
	d.io.command <- ioCheckIdle
	<-d.io.idle

	// Return the coordinates of cells that are still alive.
	alive <- finalAlive
}
