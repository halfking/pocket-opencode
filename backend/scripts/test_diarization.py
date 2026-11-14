#!/usr/bin/env python3
"""
快速测试pyannote说话人分离服务

Usage:
  python3 test_diarization.py <audio_file>
"""
import sys
import requests
import time

def test_diarization(audio_path, base_url="http://localhost:8002"):
    """测试说话人分离API"""
    print(f"Testing diarization service at {base_url}")
    
    # 1. 健康检查
    print("\n[1/3] Health check...")
    try:
        resp = requests.get(f"{base_url}/health", timeout=5)
        if resp.status_code == 200:
            print(f"  ✓ Service healthy: {resp.json()}")
        else:
            print(f"  ✗ Service unhealthy: {resp.status_code}")
            return False
    except Exception as e:
        print(f"  ✗ Cannot connect: {e}")
        print(f"  Hint: Start service with: python3 diarization_service.py")
        return False
    
    # 2. 说话人分离
    print(f"\n[2/3] Diarizing {audio_path}...")
    try:
        with open(audio_path, 'rb') as f:
            files = {'audio': f}
            data = {'min_speakers': 1, 'max_speakers': 10}
            
            t0 = time.time()
            resp = requests.post(
                f"{base_url}/diarize",
                files=files,
                data=data,
                timeout=300  # 5 minutes
            )
            elapsed = time.time() - t0
            
            if resp.status_code == 200:
                result = resp.json()
                print(f"  ✓ Diarization completed in {elapsed:.1f}s")
                print(f"  - Speaker count: {result['speaker_count']}")
                print(f"  - Segments: {len(result['segments'])}")
                print(f"  - Process time: {result['duration_ms']}ms")
                
                # 显示前5个segment
                print("\n  First 5 segments:")
                for i, seg in enumerate(result['segments'][:5]):
                    print(f"    {i+1}. {seg['speaker']}: {seg['start']:.2f}s - {seg['end']:.2f}s")
                
                return True
            else:
                print(f"  ✗ Request failed: {resp.status_code}")
                print(f"  {resp.text[:200]}")
                return False
    except Exception as e:
        print(f"  ✗ Error: {e}")
        return False
    
    # 3. 结果验证
    print("\n[3/3] Validation")
    print("  ✓ All tests passed!")
    return True

if __name__ == '__main__':
    if len(sys.argv) < 2:
        print("Usage: python3 test_diarization.py <audio_file>")
        print("\nExample:")
        print("  python3 test_diarization.py test_audio.wav")
        sys.exit(1)
    
    audio_file = sys.argv[1]
    success = test_diarization(audio_file)
    sys.exit(0 if success else 1)
