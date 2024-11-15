import { withNProgress } from '@tanem/react-nprogress'
import React from 'react'

import Bar from './bar'
import Container from './container'

const Progress: React.FC<{
    animationDuration: number
    isFinished: boolean
    progress: number
}> = ({ isFinished, progress, animationDuration }) => (
    <Container animationDuration={animationDuration} isFinished={isFinished}>
        <Bar animationDuration={animationDuration} progress={progress} />
    </Container>
)

export default withNProgress(Progress)
